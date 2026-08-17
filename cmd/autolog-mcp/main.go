package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sathyabhat/autolog/internal/mcpserver"
	"github.com/sathyabhat/autolog/internal/store"
)

func main() {
	dbPath := flag.String("db", "autolog.db", "path to the autolog SQLite database")
	timezone := flag.String("timezone", "Australia/Sydney", "timezone used for dates and times")
	addr := flag.String("addr", ":8081", "HTTP listen address")
	token := flag.String("token", os.Getenv("MCP_TOKEN"), "bearer token; defaults to MCP_TOKEN")
	flag.Parse()
	if *token == "" {
		log.Fatal("MCP token is required: use -token or MCP_TOKEN")
	}

	st, err := store.New(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	handler, err := mcpserver.HTTPHandler(st, *timezone, *token)
	if err != nil {
		log.Fatal(err)
	}

	server := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("autolog MCP HTTP server listening on %s/mcp", *addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
