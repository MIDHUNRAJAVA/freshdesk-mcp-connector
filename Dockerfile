# Two small steps: build the programs, then copy only the programs into a
# tiny image. The final image has no shell, no compiler and no source code.

# ---- Step 1: build -------------------------------------------------------
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
# CGO off gives plain static programs that run on a minimal base image.
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/freshdesk-mcp ./cmd/freshdesk-mcp \
 && go build -trimpath -ldflags="-s -w" -o /out/fake-freshdesk ./cmd/fake-freshdesk

# ---- Step 2: run ---------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/freshdesk-mcp /usr/local/bin/freshdesk-mcp
COPY --from=build /out/fake-freshdesk /usr/local/bin/fake-freshdesk
# Runs as a non-root user (set by the base image). The connector talks MCP
# over standard input and output, so it needs no open port.
ENTRYPOINT ["/usr/local/bin/freshdesk-mcp"]
