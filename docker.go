package main

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

const (
	// sessionLabel marks every container the demo owns, so leftovers can be found and removed
	sessionLabel = "pocket-id-demo.session"
	// expiresLabel holds when a session ends, so a restarted demo can take it over
	expiresLabel = "pocket-id-demo.expires"
	// templateID names the seeder container, which isn't a session
	templateID = "template"
	// signedUpMarker is created in a session's container once the visitor signed up, outside the tmpfs so Docker can see it
	signedUpMarker = "/app/.signed-up"
)

type instance struct {
	containerID string
	url         string
	apiKey      string
}

// startInstance runs a Pocket ID container on the demo network and waits until it is healthy
// Sessions get their data from the template on a size-limited tmpfs, while the seeder (no template) keeps its data on disk so it can be copied out after it stopped
func (p *pool) startInstance(ctx context.Context, id, appURL string, template []byte, expires time.Time) (inst instance, err error) {
	inst.apiKey = randomHex(32)
	seeding := template == nil

	// Docker calls ignore cancellation so a container that gets created is always known and removed again
	dockerCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()

	// Admins inside a session can't send HTTP requests to private addresses like the demo or other sessions
	config := &container.Config{
		Image: p.cfg.image,
		Env: []string{
			"APP_URL=" + appURL,
			"ENCRYPTION_KEY=" + p.encryptionKey,
			"STATIC_API_KEY=" + inst.apiKey,
			"TRUST_PROXY=true",
			"ANALYTICS_DISABLED=true",
			"VERSION_CHECK_DISABLED=true",
			"OUTBOUND_ALLOWED_HOSTS_SCIM=none",
			"OUTBOUND_ALLOWED_HOSTS_BACKCHANNEL_LOGOUT=none",
			"OUTBOUND_ALLOWED_HOSTS_FEDERATED_JWKS=none",
		},
		Labels: map[string]string{sessionLabel: id},
	}
	// Every container is capped so one visitor can't starve the host
	pidsLimit := int64(256)
	hostConfig := &container.HostConfig{
		NetworkMode: container.NetworkMode(p.cfg.network),
		Resources: container.Resources{
			Memory:     512 << 20,
			MemorySwap: 512 << 20,
			NanoCPUs:   1e9,
			PidsLimit:  &pidsLimit,
		},
		LogConfig:   container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "1"}},
		CapDrop:     []string{"ALL"},
		CapAdd:      []string{"CHOWN", "SETUID", "SETGID"},
		SecurityOpt: []string{"no-new-privileges"},
	}
	if seeding {
		config.Env = append(config.Env, "DISABLE_RATE_LIMITING=true")
	} else {
		// The tmpfs counts against the memory limit, so a visitor can't fill the host's disk with uploads
		// Docker can't copy into a tmpfs mount, so the template is streamed in by an exec and Pocket ID's own entrypoint only runs once it's there
		hostConfig.Tmpfs = map[string]string{"/app/data": "size=256m"}
		config.Labels[expiresLabel] = expires.Format(time.RFC3339)
		config.Entrypoint = []string{"sh", "-c", "until [ -e /app/.ready ]; do sleep 0.1; done; exec sh /app/docker/entrypoint.sh /app/pocket-id"}
	}

	created, err := p.docker.ContainerCreate(dockerCtx, client.ContainerCreateOptions{Name: "pocket-id-demo-" + id, Config: config, HostConfig: hostConfig})
	if err != nil {
		return inst, fmt.Errorf("create container: %w", err)
	}
	inst.containerID = created.ID
	defer func() {
		if err != nil {
			p.removeContainer(inst.containerID)
		}
	}()

	// The demo network is internal, so the seeder also joins the default bridge to fetch the logo preset index
	if seeding {
		if _, err = p.docker.NetworkConnect(dockerCtx, "bridge", client.NetworkConnectOptions{Container: inst.containerID}); err != nil {
			return inst, fmt.Errorf("connect container to the bridge network: %w", err)
		}
	}
	if _, err = p.docker.ContainerStart(dockerCtx, inst.containerID, client.ContainerStartOptions{}); err != nil {
		return inst, fmt.Errorf("start container: %w", err)
	}
	if !seeding {
		if err = p.extract(dockerCtx, inst.containerID, template); err != nil {
			return inst, err
		}
	}

	inst, err = p.inspectInstance(dockerCtx, inst.containerID)
	if err != nil {
		return inst, err
	}
	return inst, waitHealthy(ctx, inst.url)
}

// inspectInstance reads a running container's address and static API key
func (p *pool) inspectInstance(ctx context.Context, containerID string) (instance, error) {
	inst := instance{containerID: containerID}
	inspect, err := p.docker.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return inst, fmt.Errorf("inspect container: %w", err)
	}
	endpoint := inspect.Container.NetworkSettings.Networks[p.cfg.network]
	if endpoint == nil {
		return inst, fmt.Errorf("container is not on network %q", p.cfg.network)
	}
	inst.url = "http://" + net.JoinHostPort(endpoint.IPAddress.String(), "1411")
	for _, env := range inspect.Container.Config.Env {
		if key, ok := strings.CutPrefix(env, "STATIC_API_KEY="); ok {
			inst.apiKey = key
		}
	}
	return inst, nil
}

func (p *pool) markSignedUp(ctx context.Context, containerID string) error {
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err := tw.WriteHeader(&tar.Header{Name: path.Base(signedUpMarker), Mode: 0o644}); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	_, err := p.docker.CopyToContainer(ctx, containerID, client.CopyToContainerOptions{DestinationPath: path.Dir(signedUpMarker), Content: &archive})
	return err
}

func (p *pool) isSignedUp(ctx context.Context, containerID string) (bool, error) {
	_, err := p.docker.ContainerStatPath(ctx, containerID, client.ContainerStatPathOptions{Path: signedUpMarker})
	if errdefs.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// extract unpacks the template into the container's data directory and signals its entrypoint to continue
// Owners aren't restored because root has no CAP_DAC_OVERRIDE here, and Pocket ID's entrypoint chowns the directory anyway
func (p *pool) extract(ctx context.Context, containerID string, template []byte) error {
	exec, err := p.docker.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		Cmd:          []string{"sh", "-c", "tar xo -C /app && touch /app/.ready"},
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return fmt.Errorf("create template exec: %w", err)
	}
	attach, err := p.docker.ExecAttach(ctx, exec.ID, client.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("attach template exec: %w", err)
	}
	defer attach.Close()

	if _, err := io.Copy(attach.Conn, bytes.NewReader(template)); err != nil {
		return fmt.Errorf("stream template: %w", err)
	}
	if err := attach.CloseWrite(); err != nil {
		return fmt.Errorf("stream template: %w", err)
	}
	output, _ := io.ReadAll(attach.Reader)

	res, err := p.docker.ExecInspect(ctx, exec.ID, client.ExecInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspect template exec: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("extract template: exit code %d: %s", res.ExitCode, output)
	}
	return nil
}

// probeClient times out quickly because a connection attempt made before the container's network is up can hang for half a minute
var probeClient = &http.Client{Timeout: time.Second}

func waitHealthy(ctx context.Context, baseURL string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
		if err != nil {
			return err
		}
		if res, err := probeClient.Do(req); err == nil {
			res.Body.Close()
			if res.StatusCode < 300 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("instance did not become healthy: %w", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// removeContainer ignores the caller's context because cleanup must also run after a request was cancelled
func (p *pool) removeContainer(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := p.docker.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}); err != nil {
		slog.Error("Failed to remove container", "container", id, "error", err)
	}
}

// listContainers returns every demo container by its session label
func (p *pool) listContainers(ctx context.Context) (map[string]string, error) {
	list, err := p.docker.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: make(client.Filters).Add("label", sessionLabel)})
	if err != nil {
		return nil, err
	}
	containers := map[string]string{}
	for _, c := range list.Items {
		containers[c.ID] = c.Labels[sessionLabel]
	}
	return containers, nil
}

// ensureNetwork creates the internal demo network when the demo runs outside of compose, which otherwise creates it
func (p *pool) ensureNetwork(ctx context.Context) error {
	res, err := p.docker.NetworkInspect(ctx, p.cfg.network, client.NetworkInspectOptions{})
	if err == nil && !res.Network.Internal {
		return fmt.Errorf("the network must be internal so sessions can't reach the internet, remove it with `docker network rm %s` to let the demo create it", p.cfg.network)
	}
	if !errdefs.IsNotFound(err) {
		return err
	}
	slog.Info("Creating network", "network", p.cfg.network)
	_, err = p.docker.NetworkCreate(ctx, p.cfg.network, client.NetworkCreateOptions{Internal: true})
	return err
}

func (p *pool) stopContainer(ctx context.Context, id string) error {
	timeout := 30
	_, err := p.docker.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &timeout})
	return err
}

func (p *pool) copyFromContainer(ctx context.Context, id, path string) (io.ReadCloser, error) {
	res, err := p.docker.CopyFromContainer(ctx, id, client.CopyFromContainerOptions{SourcePath: path})
	return res.Content, err
}

// pullImage keeps a local copy usable when the registry is unreachable
func (p *pool) pullImage(ctx context.Context) {
	res, err := p.docker.ImagePull(ctx, p.cfg.image, client.ImagePullOptions{})
	if err == nil {
		defer res.Close()
		err = res.Wait(ctx)
	}
	if err != nil {
		slog.Warn("Failed to pull image, using the local one", "image", p.cfg.image, "error", err)
	}
}
