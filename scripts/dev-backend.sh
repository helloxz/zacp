#!/usr/bin/env bash
# Same as demo-api.sh: start Gin + reasonix --acp
ZACP_DATA="/data/apps/zacp"
cd ./backend
go run ./cmd/server -config "$ZACP_DATA/.zacp/config.toml"