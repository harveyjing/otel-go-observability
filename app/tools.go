//go:build tools

package main

import (
	_ "github.com/labstack/echo/v4"
	_ "go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
)
