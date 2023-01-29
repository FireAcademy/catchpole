package main

import (
    "os"
    "log"
    "time"
    "context"
    "google.golang.org/grpc"
    "github.com/gofiber/fiber/v2"
    pb "github.com/fireacademy/golden-gate/grpc"
    "google.golang.org/grpc/credentials/insecure"
    redis_mod "github.com/fireacademy/golden-gate/redis"
    "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
)

var client pb.GoldenGateClient

func GetAPIKeyForRequest(c *fiber.Ctx) string {
    api_key := c.Params("api_key")
    if api_key == "" {
        api_key = c.Get("X-API-Key")
    }
    if api_key == "" {
        api_key = c.Query("api-key")
    }

    return api_key
}

func CheckAPIKey(ctx context.Context, api_key string) (bool /* ok */, string /* origin */, error /* err */) {
    ok, origin, err := redis_mod.CheckAPIKeyQuickly(ctx, api_key)
    if err == nil {
        return ok, origin, nil
    }

    // not in redis - time to call golden-gate
    ctx, cancel := context.WithTimeout(ctx, 2 * time.Second)
    defer cancel()

    data := pb.RefreshAPIKeyRequest{
        APIKey: api_key,
    }
    reply, err := client.RefreshAPIKeyData(ctx, &data)
    if err == nil {
        return reply.CanBeUsed, reply.Origin, nil
    }

    log.Print(err)
    return false, "", err
}

func getGoldenGateAddress() string {
    port := os.Getenv("GOLDEN_GATE_ADDRESS")
   if port == "" {
       panic("GOLDEN_GATE_ADDRESS not set")
   }

   return port
}

func SetupRPCClient() {
    serverAddr := getGoldenGateAddress()
    conn, err := grpc.Dial(serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()),
        grpc.WithStreamInterceptor(otelgrpc.StreamClientInterceptor())
    )
    if err != nil {
        log.Print(err)
        panic(err)
    }
    // defer conn.Close()

    client = pb.NewGoldenGateClient(conn)
}
