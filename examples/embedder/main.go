// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

// Package main is an embedder-scenario smoke test: a SEPARATE module that
// depends only on github.com/oakwood-commons/celexp (+ bootstrap) and proves an
// external application can compile and evaluate CEL -- including custom extension
// functions -- without any dependency on scafctl.
//
// It is its own module (see go.mod) so it does not affect the library's own
// module graph, and it is wired into CI to catch a future scafctl-ward dependency.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/oakwood-commons/celexp"
	"github.com/oakwood-commons/celexp/bootstrap"
)

func main() {
	// One-time wiring so the full built-in + custom extension set is available.
	bootstrap.Default()

	ctx := context.Background()

	// A custom extension function (strings.slugify) -- only available because
	// bootstrap.Default() registered the env factory.
	got, err := celexp.EvaluateExpression(ctx, `strings.slugify("Hello, World!")`, nil, nil)
	if err != nil {
		log.Fatalf("eval slugify: %v", err)
	}
	if got != "hello-world" {
		log.Fatalf("slugify = %q, want %q", got, "hello-world")
	}

	// Root data (bound to _) plus an additional variable.
	greeting, err := celexp.EvaluateExpression(ctx, `prefix + " " + _.name`,
		map[string]any{"name": "celexp"},
		map[string]any{"prefix": "Hello"},
	)
	if err != nil {
		log.Fatalf("eval greeting: %v", err)
	}
	if greeting != "Hello celexp" {
		log.Fatalf("greeting = %q, want %q", greeting, "Hello celexp")
	}

	fmt.Println("embedder OK:", got, "|", greeting)
}
