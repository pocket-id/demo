# Pocket ID demo

Gives every visitor their own temporary [Pocket ID](https://github.com/pocket-id/pocket-id) instance, filled with seed data.
Visitors go through the normal setup to create their admin account, and the instance is deleted when the session ends.

## Getting started

```sh
docker compose up -d --build
```

Open http://localhost:8080 and click "Start the demo".
Each demo runs on its own subdomain like `3f9a1c2e.localhost`, which browsers resolve without any DNS setup.

In production, set `BASE_URL` and point DNS for both your host and `*.your-host` at the demo.

## Configuration

| Variable              | Default                               | Description                                          |
| --------------------- | ------------------------------------- | ---------------------------------------------------- |
| `BASE_URL`            | `http://localhost:8080`               | Public URL of the demo                               |
| `PORT`                | `8080`                                | Port to listen on                                    |
| `POCKET_ID_IMAGE`     | `ghcr.io/pocket-id/pocket-id:next-v3` | Pocket ID image used for the demos                   |
| `DOCKER_NETWORK`      | `pocket-id-demo`                      | Docker network shared by the demo and its instances  |
| `SESSION_DURATION`    | `1h`                                  | How long a demo lives                                |
| `MAX_SESSIONS`        | `30`                                  | Maximum number of demos running at the same time     |
| `RATE_LIMIT_DEMOS`    | `5`                                   | Demos an IP can start in a row, `0` turns it off     |
| `RATE_LIMIT_INTERVAL` | `15m`                                 | How long an IP then waits for each further demo      |
| `TRUST_PROXY`         | `false`                               | Set to `true` behind a reverse proxy                 |

## How it works

- On startup and once a day, the demo seeds one Pocket ID instance and keeps a copy of its data as a template.
- Every new demo starts a container from that template, so it's ready in about two seconds.
- Demos run on an internal network without internet access and with memory, CPU and disk limits, because visitors are admins inside them.
- Restarting or updating the demo keeps running demos, so `docker compose down` leaves them and their network in place. To remove them, run `docker rm -f $(docker ps -aq --filter label=pocket-id-demo.session)`.
- The demo answers Pocket ID's setup endpoints itself until the visitor has signed up, because the seeded users would otherwise close the setup.
