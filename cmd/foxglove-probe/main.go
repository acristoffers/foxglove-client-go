// Command foxglove-probe connects to a Foxglove WebSocket server, prints what
// it advertises, and optionally streams topics while reporting their rates.
//
//	foxglove-probe -url ws://robot:8765
//	foxglove-probe -url ws://robot:8765 /odom /scan
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"text/tabwriter"
	"time"

	"github.com/acristoffers/foxglove-client-go/foxglove"
)

func main() {
	url := flag.String("url", "ws://localhost:8765", "WebSocket URL of the Foxglove server")
	wait := flag.Duration("wait", 2*time.Second, "how long to collect advertisements before listing them")
	duration := flag.Duration("duration", 0, "how long to stream topics (0 streams until interrupted)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: %s [flags] [topic...]\n\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if err := run(*url, *wait, *duration, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(url string, wait, duration time.Duration, topics []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	client, err := foxglove.Dial(dialCtx, url, nil)
	cancel()
	if err != nil {
		return err
	}
	defer client.Close()

	info := client.ServerInfo()
	fmt.Printf("server:       %s\n", info.Name)
	fmt.Printf("subprotocol:  %s\n", client.Subprotocol())
	fmt.Printf("capabilities: %v\n", info.Capabilities)
	fmt.Printf("encodings:    %v\n", info.SupportedEncodings)
	if info.SessionID != "" {
		fmt.Printf("session:      %s\n", info.SessionID)
	}
	for k, v := range info.Metadata {
		fmt.Printf("metadata:     %s=%s\n", k, v)
	}

	// Advertisements arrive as events right after connecting.
	collect := time.After(wait)
collecting:
	for {
		select {
		case ev, ok := <-client.Events():
			if !ok {
				return client.Err()
			}
			report(ev)
		case <-collect:
			break collecting
		case <-ctx.Done():
			return nil
		}
	}

	printChannels(client.Channels())
	printServices(client.Services())

	if len(topics) == 0 {
		return nil
	}
	return stream(ctx, client, topics, duration)
}

type topicStats struct {
	topic     string
	channelID uint64
	messages  int
	bytes     int
}

func stream(ctx context.Context, client *foxglove.Client, topics []string, duration time.Duration) error {
	wanted := map[string]bool{}
	for _, topic := range topics {
		wanted[topic] = true
	}
	subs := map[uint32]*topicStats{}

	subscribe := func(ch *foxglove.Channel) {
		if !wanted[ch.Topic] {
			return
		}
		for _, s := range subs {
			if s.topic == ch.Topic {
				return
			}
		}
		id, err := client.Subscribe(ch.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "subscribe %s: %v\n", ch.Topic, err)
			return
		}
		subs[id] = &topicStats{topic: ch.Topic, channelID: ch.ID}
	}

	for _, ch := range client.Channels() {
		subscribe(ch)
	}
	for topic := range wanted {
		if _, ok := client.ChannelByTopic(topic); !ok {
			fmt.Fprintf(os.Stderr, "%s is not advertised yet, waiting for it\n", topic)
		}
	}

	var deadline <-chan time.Time
	if duration > 0 {
		deadline = time.After(duration)
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case ev, ok := <-client.Events():
			if !ok {
				return client.Err()
			}
			switch ev := ev.(type) {
			case foxglove.MessageData:
				if s := subs[ev.SubscriptionID]; s != nil {
					s.messages++
					s.bytes += len(ev.Data)
				}
			case foxglove.Advertise:
				// Topics that went away and came back must be subscribed to again.
				for _, ch := range ev.Channels {
					subscribe(&ch)
				}
			case foxglove.Unadvertise:
				for _, id := range ev.ChannelIDs {
					for subID, s := range subs {
						if s.channelID == id {
							fmt.Fprintf(os.Stderr, "%s was unadvertised\n", s.topic)
							delete(subs, subID)
						}
					}
				}
			default:
				report(ev)
			}

		case <-ticker.C:
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
			for _, s := range subs {
				fmt.Fprintf(w, "%s\t%d msg/s\t%.1f KiB/s\t\n", s.topic, s.messages, float64(s.bytes)/1024)
				s.messages, s.bytes = 0, 0
			}
			w.Flush()
			fmt.Println()

		case <-deadline:
			return nil
		case <-ctx.Done():
			return nil
		}
	}
}

// report prints events that deserve attention while probing.
func report(ev foxglove.Event) {
	switch ev := ev.(type) {
	case foxglove.Status:
		fmt.Fprintf(os.Stderr, "status [%s]: %s\n", ev.Level, ev.Message)
	case foxglove.ProtocolError:
		fmt.Fprintln(os.Stderr, ev.Error())
	}
}

func printChannels(channels []*foxglove.Channel) {
	fmt.Printf("\n%d channels\n", len(channels))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTOPIC\tTYPE\tENCODING\tSCHEMA")
	for _, ch := range channels {
		schema := ch.SchemaEncoding
		if schema == "" {
			schema = "-"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s (%d B)\n", ch.ID, ch.Topic, ch.SchemaName, ch.Encoding, schema, len(ch.Schema))
	}
	w.Flush()
}

func printServices(services []*foxglove.Service) {
	if len(services) == 0 {
		return
	}
	fmt.Printf("\n%d services\n", len(services))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tTYPE\tENCODING")
	for _, svc := range services {
		encoding := "-"
		if svc.Request != nil {
			encoding = svc.Request.Encoding
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", svc.ID, svc.Name, svc.Type, encoding)
	}
	w.Flush()
}
