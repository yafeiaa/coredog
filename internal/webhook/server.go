package webhook

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

type Server struct {
	server *http.Server
	cert   string
	key    string
	port   int
}

func NewServer(certFile, keyFile string, port int) *Server {
	return &Server{
		cert: certFile,
		key:  keyFile,
		port: port,
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Mutating webhook endpoint
	mutateHandler := NewMutateHandler()
	mux.HandleFunc("/mutate", mutateHandler.ServeHTTP)

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Ready check endpoint
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ready"))
	})

	s.server = &http.Server{
		Addr:           fmt.Sprintf(":%d", s.port),
		Handler:        mux,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1MB
	}

	// Load TLS certificate
	cert, err := tls.LoadX509KeyPair(s.cert, s.key)
	if err != nil {
		return fmt.Errorf("failed to load TLS certificate: %w", err)
	}

	s.server.TLSConfig = &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	logrus.Infof("Starting webhook server on port %d", s.port)

	if err := s.server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start webhook server: %w", err)
	}

	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	logrus.Info("Shutting down webhook server")
	return s.server.Shutdown(ctx)
}
