# CLAUDE.md — http2dns

This file provides context for AI-assisted development on the `http2dns` project.

---

## Project overview

`http2dns` is a single-binary HTTP gateway that exposes DNS resolution as a JSON REST API.
It is written entirely in Go and embeds all static assets (web UI, favicon, OpenAPI spec) at compile time using `//go:embed` directives, so the resulting binary has zero runtime file dependencies.

The server accepts `POST /api/v1/dns` requests describing a DNS query and returns resolved records as structured JSON. It supports UDP with automatic TCP fallback for truncated responses.

---

## Repository layout

```
.
├── api/
│   └── swagger.yaml              # OpenAPI 3.1 source (human-editable)
├── build/
│   └── Dockerfile                # Two-stage Docker build (builder + scratch runtime)
├── cmd/
│   └── http2dns/
│       ├── main.go               # Entire application — single file
│       └── static/
│           ├── favicon.png       # Embedded at build time
│           ├── index.html        # Embedded web UI (dark/light, 15 languages)
│           └── openapi.json      # Embedded OpenAPI spec (generated from swagger.yaml)
├── scripts/
│   ├── 000_init.sh               # go mod tidy
│   ├── 999_test.sh               # Integration smoke tests (curl + jq)
│   ├── linux_build.sh            # Native static binary build
│   ├── linux_run.sh              # Run binary on Linux
│   ├── docker_build.sh           # Build Docker image
│   ├── docker_run.sh             # Run Docker container
│   ├── windows_build.cmd         # Native build on Windows
│   └── windows_run.cmd           # Run binary on Windows
├── go.mod
├── go.sum
├── LICENSE                       # Apache 2.0
├── README.md
└── CLAUDE.md                     # This file
```

---

## Key design decisions

- **Single `main.go`**: the entire server logic lives in `cmd/http2dns/main.go`. There are no internal packages. Keep it that way unless the file grows substantially.
- **Embedded assets**: `favicon.png`, `index.html`, and `openapi.json` are embedded with `//go:embed`. Any change to these files is picked up at the next `go build` — no copy step needed.
- **Static binary**: the build uses `-tags netgo` and `-ldflags "-extldflags -static"` to produce a fully self-contained binary with no libc dependency. Do not introduce `cgo` dependencies.
- **No framework**: the HTTP layer uses only the standard library (`net/http`). Do not add a router or web framework.
- **DNS library**: [`github.com/miekg/dns`](https://github.com/miekg/dns) is the only non-stdlib dependency. It handles message construction, UDP/TCP exchange, and record parsing.
- **UDP → TCP fallback**: when the DNS response has the TC (truncated) bit set, the server automatically retries over TCP. This is required for large TXT records.
- **Per-request DNS server list**: callers may supply their own `dnsservers` list. If omitted, the server falls back to the `DNS_SERVERS` environment variable, then to Google public DNS (`8.8.8.8:53`, `8.8.4.4:53`).

---

## Environment variables & CLI flags

Every configuration value can be set via an environment variable **or** a command-line flag. The flag always takes priority. Resolution order: **CLI flag → environment variable → hard-coded default**.

| Environment variable | CLI flag          | Default                    | Description                                              |
|----------------------|-------------------|----------------------------|----------------------------------------------------------|
| `LISTEN_ADDR`        | `--listen-addr`   | `127.0.0.1:8080`           | Listen address. A bare port (e.g. `8080`) is accepted.  |
| `DNS_SERVERS`        | `--dns-servers`   | `8.8.8.8:53,8.8.4.4:53`   | Comma-separated fallback DNS servers (`host:port`).      |

CLI flags are parsed with the standard library `flag` package. Any new configuration entry must expose both a flag and its environment variable counterpart.

---

## Build & run commands

```bash
# Initialise / tidy dependencies
bash scripts/000_init.sh

# Build native static binary → ./out/http2dns
bash scripts/linux_build.sh

# Run (sets LISTEN_ADDR=0.0.0.0:8080)
bash scripts/linux_run.sh

# Build Docker image → letstool/http2dns:latest
bash scripts/docker_build.sh

# Run Docker container
bash scripts/docker_run.sh

# Smoke tests (server must be running)
bash scripts/999_test.sh
```

---

## API contract

### Endpoint

```
POST /api/v1/dns
Content-Type: application/json
```

### Request fields

| Field        | Type       | Required | Notes                                                        |
|--------------|------------|----------|--------------------------------------------------------------|
| `class`      | `string`   | ✅       | `IN` \| `CH` \| `HS` \| `CS`                                |
| `type`       | `string`   | ✅       | `A` `AAAA` `CNAME` `MX` `NS` `PTR` `SOA` `TXT` `SRV` `NAPTR` `OPT` `ANY` |
| `record`     | `string`   | ✅       | DNS name to query                                            |
| `dnsservers` | `string[]` | ❌       | Per-request DNS servers (`host:port`)                        |
| `timeout`    | `int`      | ❌       | Seconds, default `5`, range `1–60`                           |

### Response status values

| Value      | Meaning                                     |
|------------|---------------------------------------------|
| `SUCCESS`  | Query resolved — `answers` is populated     |
| `NXDOMAIN` | Domain does not exist                       |
| `ERROR`    | Bad request or network failure              |
| `TMOUT`    | All DNS servers timed out                   |

### Other endpoints

| Method | Path           | Description                        |
|--------|----------------|------------------------------------|
| `GET`  | `/`            | Embedded interactive web UI        |
| `GET`  | `/openapi.json`| OpenAPI 3.1 specification          |
| `GET`  | `/favicon.png` | Application icon                   |

---

## Web UI

The UI is a self-contained single-file HTML/JS/CSS application embedded in the binary.

- **Themes**: dark and light, switchable via a toggle button.
- **Languages**: 15 locales built in — Arabic (`ar`), Bengali (`bn`), Chinese (`zh`), German (`de`), English (`en`), Spanish (`es`), French (`fr`), Hindi (`hi`), Indonesian (`id`), Japanese (`ja`), Korean (`ko`), Portuguese (`pt`), Russian (`ru`), Urdu (`ur`), Vietnamese (`vi`). Language is selected from a dropdown.
- **RTL support**: Arabic and Urdu automatically switch the layout to right-to-left.
- The UI calls `POST /api/v1/dns` and renders results in a table.
- The OpenAPI spec is also served at `/openapi.json` for use with tools such as Swagger UI or Postman.

To modify the UI, edit `cmd/http2dns/static/index.html` and rebuild.  
To update the API spec, edit `api/swagger.yaml`, regenerate `openapi.json`, and rebuild.

---

## Adding a new DNS record type

1. Add the new type string to `dnsTypeFromString()` in `main.go`.
2. Add a corresponding `case` in `rrToAnswer()` to extract the record-specific fields into the `data` string.
3. Update `api/swagger.yaml` (the `DNSType` enum) and regenerate `openapi.json`.
4. Update the `<select>` element in `cmd/http2dns/static/index.html` if it hard-codes the type list.
5. Rebuild.

---

## Constraints & conventions

- Go version: **1.24+**
- No `cgo`. Keep `CGO_ENABLED=0`.
- No additional HTTP frameworks or routers.
- All logic stays in `cmd/http2dns/main.go` unless a strong reason arises to split it.
- Error responses always return a `DNSQueryResponse` JSON body — never a plain-text error.
- The server never logs request bodies; avoid adding logging that could expose user-queried domains.
- All code, identifiers, comments, and documentation must be written in **English**. No icons, emoji, or non-ASCII decorations in comments or doc strings.
- **Every configuration environment variable must have a corresponding command-line flag** (parsed via `flag` from the standard library). The flag always takes priority over the environment variable. The resolution order is: CLI flag → environment variable → hard-coded default. For example, `LISTEN_ADDR` must be overridable with `--listen-addr` (or `-listen-addr`), and `DNS_SERVERS` with `--dns-servers`.

---

## AI-assisted development

This project was developed with the assistance of **Claude Sonnet 4.6** by Anthropic.
