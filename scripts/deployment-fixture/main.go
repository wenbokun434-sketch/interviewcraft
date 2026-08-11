// Command deployment-fixture provides loopback-only release/Provider fixtures
// and a test-mode Cosign stand-in for deployment E2E. It is never shipped.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "verify-blob" {
		if err := verifyBlob(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := serve(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve(arguments []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	root := flags.String("root", "", "release fixture root")
	readyFile := flags.String("ready-file", "", "file receiving the loopback base URL")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 || *root == "" || *readyFile == "" {
		return errors.New("usage: deployment-fixture --root DIR --ready-file FILE")
	}
	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	files := http.FileServer(http.Dir(absoluteRoot))
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(response http.ResponseWriter, _ *http.Request) {
		payload, readErr := os.ReadFile(filepath.Join(absoluteRoot, "latest.txt"))
		if readErr != nil {
			http.Error(response, "latest fixture unavailable", http.StatusServiceUnavailable)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]string{"tag_name": "v" + strings.TrimSpace(string(payload))})
	})
	mux.HandleFunc("/models", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"data":[{"id":"fixture-model"}]}`))
	})
	mux.HandleFunc("/api/tags", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"models":[{"name":"fixture-model"}]}`))
	})
	mux.Handle("/", files)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	baseURL := "http://" + listener.Addr().String()
	if err := os.MkdirAll(filepath.Dir(*readyFile), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(*readyFile, []byte(baseURL+"\n"), 0o600); err != nil {
		return err
	}
	return server.Serve(listener)
}

func verifyBlob(arguments []string) error {
	value := func(name string) string {
		for index := 0; index+1 < len(arguments); index++ {
			if arguments[index] == name {
				return arguments[index+1]
			}
		}
		return ""
	}
	bundlePath := value("--bundle")
	identity := value("--certificate-identity")
	issuer := value("--certificate-oidc-issuer")
	if bundlePath == "" || identity == "" || issuer == "" || len(arguments) == 0 {
		return errors.New("fixture verifier received incomplete arguments")
	}
	payload, err := os.ReadFile(bundlePath)
	if err != nil {
		return err
	}
	want := "VALID\n" + identity + "\n" + issuer + "\n"
	if string(payload) != want {
		return errors.New("fixture bundle identity or issuer is invalid")
	}
	manifest := arguments[len(arguments)-1]
	info, err := os.Stat(manifest)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("fixture manifest is unavailable")
	}
	return nil
}
