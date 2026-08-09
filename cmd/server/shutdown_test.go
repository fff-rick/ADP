package main

import (
	"context"
	"net"
	"testing"
	"time"

	adpv1 "adp/api/proto/adp/v1"
	"adp/internal/infrastructure/db"
	"adp/internal/infrastructure/workerstream"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

func TestStopGRPCServerForcesActiveWorkerStreamAfterTimeout(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	defer listener.Close() //nolint:errcheck

	server := grpc.NewServer()
	hub := workerstream.NewHub()
	adpv1.RegisterWorkerServiceServer(server, workerstream.NewService(db.NewMemoryRepository(), "worker-secret", hub))
	go func() { _ = server.Serve(listener) }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial grpc server: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	streamCtx := metadata.AppendToOutgoingContext(ctx, "x-worker-token", "worker-secret")
	stream, err := adpv1.NewWorkerServiceClient(conn).Stream(streamCtx)
	if err != nil {
		t.Fatalf("open worker stream: %v", err)
	}
	if err := stream.Send(&adpv1.WorkerEnvelope{Payload: &adpv1.WorkerEnvelope_Register{Register: &adpv1.RegisterRequest{Name: "worker-1", WorkerType: "shell"}}}); err != nil {
		t.Fatalf("register worker: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("receive registration confirmation: %v", err)
	}

	start := time.Now()
	stopGRPCServer(server, 20*time.Millisecond)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("gRPC stop took %s with an active worker stream", elapsed)
	}
}
