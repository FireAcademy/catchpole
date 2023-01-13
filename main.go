package main

import (
	"os"
	"fmt"
	"log"
	"github.com/gofiber/fiber/v2"
)

func getPort() string {
    port := os.Getenv("CATCHPOLE_LISTEN_PORT")
   if port == "" {
       panic("CATCHPOLE_LISTEN_PORT not set")
   }

   return port
}

func Index(c *fiber.Ctx) error {
	return c.SendString("Catchpole is alive and well.")
}


func main() {
	SetupConfig()
	app := fiber.New()

	app.Get("/", Index)

	port := getPort()
	log.Fatalln(app.Listen(fmt.Sprintf(":%v", port)))
}