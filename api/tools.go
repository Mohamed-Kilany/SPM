//go:build tools

// Package main contains build-time tool and dependency pins.
// This file ensures that all required direct dependencies are recorded in
// go.mod even before every package is fully imported in production code.
package main

import (
	_ "github.com/golang-jwt/jwt/v5"
	_ "github.com/jackc/pgx/v5"
	_ "github.com/leanovate/gopter"
	_ "github.com/nats-io/nats.go"
	_ "github.com/pierrec/lz4/v4"
	_ "github.com/stretchr/testify/assert"
)
