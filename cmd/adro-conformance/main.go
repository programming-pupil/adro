// adro-conformance performs the authenticated WebSocket portion of the real
// provider acceptance suite. It emits only a digest and event shape so provider
// tokens and potentially sensitive event payloads never enter the report.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	target := flag.String("websocket-url", "", "Multica daemon WebSocket URL")
	runID := flag.String("run-id", "", "provider run/task ID")
	cursor := flag.String("cursor", "", "optional event cursor")
	readyFile := flag.String("ready-file", "", "write this file after the WebSocket handshake succeeds")
	timeout := flag.Duration("timeout", 30*time.Second, "maximum wait for one event")
	flag.Parse()
	if *target == "" || *runID == "" || os.Getenv("ADRO_MULTICA_TOKEN") == "" {
		fatal("websocket URL, run ID, and ADRO_MULTICA_TOKEN are required")
	}
	u, err := url.Parse(*target)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		fatal("websocket URL must use ws or wss")
	}
	query := u.Query()
	query.Set("run_id", *runID)
	if *cursor != "" {
		query.Set("cursor", *cursor)
	}
	u.RawQuery = query.Encode()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	header := http.Header{"Authorization": []string{"Bearer " + os.Getenv("ADRO_MULTICA_TOKEN")}}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), header)
	if err != nil {
		fatal("daemon WebSocket connection failed")
	}
	defer conn.Close()
	if *readyFile != "" {
		if err := os.WriteFile(*readyFile, []byte("ready\n"), 0600); err != nil {
			fatal("daemon WebSocket ready signal could not be written")
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(*timeout))
	var event map[string]any
	if err := conn.ReadJSON(&event); err != nil {
		fatal("daemon WebSocket produced no valid event before timeout")
	}
	data, _ := json.Marshal(event)
	digest := sha256.Sum256(data)
	keys := make([]string, 0, len(event))
	for key := range event {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"status": "passed", "event_sha256": hex.EncodeToString(digest[:]), "event_fields": keys})
}

func fatal(message string) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"status": "blocked", "reason": message})
	fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}
