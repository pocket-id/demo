package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"
)

// keepTemplateFresh rebuilds the template daily because the seeded history is relative to the day it was built
// Failed builds are retried with a growing delay, since each attempt downloads assets from third-party servers
func (p *pool) keepTemplateFresh(ctx context.Context) {
	backoff := time.Minute
	for {
		wait := 24 * time.Hour
		if err := p.buildTemplate(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("Failed to build the data template", "error", err, "retryIn", backoff)
			wait = backoff
			backoff = min(backoff*2, time.Hour)
		} else {
			backoff = time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (p *pool) buildTemplate(ctx context.Context) error {
	started := time.Now()
	slog.Info("Building the data template")
	p.building.Store(true)
	defer p.building.Store(false)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	p.pullImage(ctx)

	// Seed the entities through the API of a fresh instance
	inst, err := p.startInstance(ctx, templateID, "http://localhost:1411", nil, time.Time{})
	if err != nil {
		return err
	}
	defer p.removeContainer(inst.containerID)
	rng := rand.New(rand.NewPCG(1337, 0))
	ids, err := seedEntities(ctx, inst, rng)
	if err != nil {
		return fmt.Errorf("seed: %w", err)
	}

	// Stop the instance so the database is complete on disk, then copy the data directory out
	if err := p.stopContainer(ctx, inst.containerID); err != nil {
		return fmt.Errorf("stop container: %w", err)
	}
	dir, err := os.MkdirTemp("", "pocket-id-demo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	content, err := p.copyFromContainer(ctx, inst.containerID, "/app/data")
	if err != nil {
		return fmt.Errorf("copy data out of container: %w", err)
	}
	err = untar(content, dir)
	content.Close()
	if err != nil {
		return err
	}

	// Add the history the API can't backdate
	if err := writeHistory(filepath.Join(dir, "data", "pocket-id.db"), ids, rng, time.Now()); err != nil {
		return fmt.Errorf("write history: %w", err)
	}

	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err := tw.AddFS(os.DirFS(dir)); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	data := archive.Bytes()
	p.template.Store(&data)
	slog.Info("Data template ready", "duration", time.Since(started).Round(time.Second), "bytes", archive.Len())
	return nil
}

func untar(r io.Reader, dir string) error {
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if !filepath.IsLocal(header.Name) {
			return fmt.Errorf("unexpected path %q in archive", header.Name)
		}
		path := filepath.Join(dir, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(path, 0o755)
		case tar.TypeReg:
			err = writeFile(path, tr)
		}
		if err != nil {
			return err
		}
	}
}

func writeFile(path string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
