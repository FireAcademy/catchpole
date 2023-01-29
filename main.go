package main

import (
	"os"
	"fmt"
	"log"
	"context"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/contrib/otelfiber"
	telemetry "github.com/fireacademy/telemetry"
	"github.com/gofiber/fiber/v2/middleware/cors"
	redis_mod "github.com/fireacademy/golden-gate/redis"
)

func Index(c *fiber.Ctx) error {
	return c.SendString("Catchpole is alive and well.")
}

func getPort() string {
    port := os.Getenv("CATCHPOLE_LISTEN_PORT")
   if port == "" {
       panic("CATCHPOLE_LISTEN_PORT not set")
   }

   return port
}

func main() {
	cleanup := telemetry.Initialize()
	defer cleanup(context.Background())

	SetupConfig()
	redis_mod.SetupRedis()
	SetupRPCClient()

	app := fiber.New()
	app.Use(otelfiber.Middleware())
	app.Use(cors.New())

	app.Get("/", Index)
	app.Get("/:api_key<guid>/:route/*", HandleRequest)
	app.Post("/:api_key<guid>/:route/*", HandleRequest)
	app.Put("/:api_key<guid>/:route/*", HandleRequest)
	app.Get("/:route/*", HandleRequest)
	app.Post("/:route/*", HandleRequest)
	app.Put("/:route/*", HandleRequest)

	port := getPort()
	log.Fatalln(app.Listen(fmt.Sprintf(":%v", port)))
}