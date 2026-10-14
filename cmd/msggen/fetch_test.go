package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/acristoffers/foxglove-client-go/foxglove"
	"github.com/coder/websocket"
)

func TestValidSchemaName(t *testing.T) {
	valid := []string{"geometry_msgs/msg/Point", "tf2_msgs/msg/TFMessage", "pkg/msg/A1"}
	for _, name := range valid {
		if !validSchemaName(name) {
			t.Errorf("%q rejected", name)
		}
	}
	invalid := []string{
		"", "geometry_msgs/Point", "pkg/srv/Type", "../../outside", "pkg/msg/../../outside",
		"../msg/Type", "pkg/msg/", "/msg/Type", "pkg/msg/a/b", `pkg/msg/a\b`, "pkg/msg/1Type",
		"pkg/msg/.hidden", "/etc/msg/passwd",
	}
	for _, name := range invalid {
		if validSchemaName(name) {
			t.Errorf("%q accepted", name)
		}
	}
}

// bridge serves a fake foxglove_bridge that advertises the given channels.
func bridge(t *testing.T, channels string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{foxglove.SubprotocolSDK}})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := r.Context()
		conn.Write(ctx, websocket.MessageText, []byte(`{"op":"serverInfo","name":"fake","capabilities":[],"supportedEncodings":["cdr"]}`))
		conn.Write(ctx, websocket.MessageText, []byte(`{"op":"advertise","channels":`+channels+`}`))
		conn.Read(context.Background())
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestFetchRejectsTraversal(t *testing.T) {
	url := bridge(t, `[
		{"id":1,"topic":"/good","encoding":"cdr","schemaName":"pkg/msg/Good","schemaEncoding":"ros2msg","schema":"int32 a"},
		{"id":2,"topic":"/evil","encoding":"cdr","schemaName":"../../outside","schemaEncoding":"ros2msg","schema":"int32 a"}
	]`)
	base := t.TempDir()
	dir := filepath.Join(base, "a", "schemas")

	err := fetchCommand([]string{"-url", url, "-dir", dir, "/good", "/evil"})
	if err == nil || !strings.Contains(err.Error(), "../../outside") {
		t.Fatalf("fetch = %v, want an error naming the bad schema", err)
	}
	if _, err := os.Stat(filepath.Join(base, "outside.msg")); err == nil {
		t.Error("schema written outside the schema directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "pkg", "msg", "Good.msg")); err == nil {
		t.Error("schemas written although one of them was rejected")
	}
}

func TestFetch(t *testing.T) {
	url := bridge(t, `[{"id":1,"topic":"/good","encoding":"cdr","schemaName":"pkg/msg/Good","schemaEncoding":"ros2msg","schema":"int32 a"}]`)
	dir := filepath.Join(t.TempDir(), "schemas")
	if err := fetchCommand([]string{"-url", url, "-dir", dir, "/good"}); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(filepath.Join(dir, "pkg", "msg", "Good.msg"))
	if err != nil || string(text) != "int32 a\n" {
		t.Errorf("Good.msg = %q, %v", text, err)
	}
}
