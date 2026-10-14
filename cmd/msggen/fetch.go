package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/acristoffers/foxglove-client-go/foxglove"
	"github.com/acristoffers/foxglove-client-go/ros2msg"
)

func fetchCommand(args []string) error {
	flags := flag.NewFlagSet("fetch", flag.ExitOnError)
	url := flags.String("url", "ws://localhost:8765", "WebSocket URL of foxglove_bridge")
	dir := flags.String("dir", "schemas", "schema directory")
	timeout := flags.Duration("timeout", 5*time.Second, "how long to wait for the requested types to be advertised")
	flags.Parse(args)

	// Types are wanted in "pkg/msg/Type" form, the one channels advertise.
	wanted := map[string]bool{}
	var topics []string
	for _, arg := range flags.Args() {
		if strings.HasPrefix(arg, "/") {
			topics = append(topics, arg)
		} else {
			wanted[schemaName(ros2msg.Normalize(arg))] = true
		}
	}
	if len(flags.Args()) == 0 {
		existing, err := existingTypes(*dir)
		if err != nil {
			return err
		}
		if len(existing) == 0 {
			return fmt.Errorf("nothing to fetch: %s holds no schemas, name topics or types to fetch", *dir)
		}
		for _, name := range existing {
			wanted[name] = true
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	client, err := foxglove.Dial(ctx, *url, nil)
	if err != nil {
		return err
	}
	defer client.Close()

	schemas := map[string]string{}
	pendingTopics := slices.Clone(topics)
	complete := func() bool {
		for _, ch := range client.Channels() {
			if ch.Encoding != "cdr" || ch.SchemaEncoding != "ros2msg" {
				continue
			}
			if i := slices.Index(pendingTopics, ch.Topic); i >= 0 {
				pendingTopics = slices.Delete(pendingTopics, i, i+1)
				wanted[ch.SchemaName] = true
			}
			if wanted[ch.SchemaName] {
				schemas[ch.SchemaName] = ch.Schema
			}
		}
		return len(pendingTopics) == 0 && len(schemas) == len(wanted)
	}

wait:
	for !complete() {
		select {
		case _, ok := <-client.Events():
			if !ok {
				return client.Err()
			}
		case <-ctx.Done():
			break wait
		}
	}

	// Schema names come from the server and become file paths, so they are
	// checked before anything is written, and writes go through an os.Root
	// that cannot escape dir.
	names := slices.Sorted(maps.Keys(schemas))
	for _, name := range names {
		if !validSchemaName(name) {
			return fmt.Errorf("refusing to write schema %q: not of the form package/msg/Type", name)
		}
	}
	if len(names) > 0 {
		if err := os.MkdirAll(*dir, 0o755); err != nil {
			return err
		}
		root, err := os.OpenRoot(*dir)
		if err != nil {
			return err
		}
		defer root.Close()
		for _, name := range names {
			path := filepath.FromSlash(name) + ".msg"
			if err := root.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			text := strings.TrimRight(schemas[name], "\n") + "\n"
			if err := root.WriteFile(path, []byte(text), 0o644); err != nil {
				return err
			}
			fmt.Println("wrote", filepath.Join(*dir, path))
		}
	}

	var missing []string
	for _, topic := range pendingTopics {
		missing = append(missing, "topic "+topic)
	}
	for name := range wanted {
		if _, ok := schemas[name]; !ok {
			missing = append(missing, "type "+name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Errorf("not advertised as cdr/ros2msg by the bridge: %s", strings.Join(missing, ", "))
	}
	return nil
}

// existingTypes lists the types that have a schema file in dir.
func existingTypes(dir string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".msg") {
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			names = append(names, filepath.ToSlash(strings.TrimSuffix(rel, ".msg")))
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return names, err
}

// schemaName turns a normalized "pkg/Type" name into "pkg/msg/Type".
func schemaName(normalized string) string {
	pkg, name, _ := strings.Cut(normalized, "/")
	return pkg + "/msg/" + name
}

// validSchemaName reports whether name is of the form "pkg/msg/Type", with
// package and type names made of letters, digits and underscores, so that it
// is safe to use as a path.
func validSchemaName(name string) bool {
	parts := strings.Split(name, "/")
	if len(parts) != 3 || parts[1] != "msg" {
		return false
	}
	for _, part := range []string{parts[0], parts[2]} {
		if part == "" || !isLetter(part[0]) {
			return false
		}
		for _, c := range []byte(part) {
			if !isLetter(c) && !('0' <= c && c <= '9') && c != '_' {
				return false
			}
		}
	}
	return true
}

func isLetter(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }
