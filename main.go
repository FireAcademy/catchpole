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

func HandleRequest(c *fiber.Ctx) error {
	route := c.Params("route")
	path := c.Params("*")

	route := getRoute(route)
	cost := getCost(route, path)
	fmt.Println(route)
	fmt.Println(cost)

	return c.SendString(route + "." + path)
}

func main() {
	SetupConfig()
	app := fiber.New()

	app.Get("/", Index)
	app.Get("/:route/*", HandleRequest)
	app.Post("/:route/*", HandleRequest)
	app.Put("/:route/*", HandleRequest)

	port := getPort()
	log.Fatalln(app.Listen(fmt.Sprintf(":%v", port)))
}