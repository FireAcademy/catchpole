package main

import (
   "fmt"
   "log"
   "os"

   "github.com/gofiber/fiber/v2"
   "github.com/gofiber/fiber/v2/middleware/monitor"
   "github.com/gofiber/fiber/v2/middleware/basicauth"
)

func leafletHandler(c *fiber.Ctx, api_key string, endpoint string) error {
    return c.SendString(api_key + ":" + endpoint)
}

func leafletRouteWithAPIKeyHandler(c *fiber.Ctx) error {
    api_key := c.Params("api_key")
    endpoint := c.Params("endpoint")

    c.Set("X-API-Key", api_key)
    return leafletHandler(c, api_key, endpoint)
}

func leafletRouteWithoutAPIKeyHandler(c *fiber.Ctx) error {
    api_key := c.Query("api-key")
    if api_key == "" {
        api_key = c.Get("X-API-Key")
    }
    endpoint := c.Params("endpoint")

    return leafletHandler(c, api_key, endpoint)
}

func main() {
   app := fiber.New()
   port := os.Getenv("TAXMAN_PORT")
   if port == "" {
       port = "5000"
   }

   // Leaflet host & port
   leaflet_host := os.Getenv("LEAFLET_HOST")
   if leaflet_host == "" {
       leaflet_host = "leaflet"
   }
   leaflet_port := os.Getenv("LEAFLET_PORT")
   if leaflet_port == "" {
       leaflet_port = "18444"
   }
   fmt.Printf("Leaflet at http://%s:%s\n", leaflet_host, leaflet_port)


   app.Get("/", func(c *fiber.Ctx) error {
        return c.SendString("Taxman is alive and well.")
    })

    app.Get("/:api_key<guid>/leaflet/:endpoint", leafletRouteWithAPIKeyHandler)
    app.Post("/:api_key<guid>/leaflet/:endpoint", leafletRouteWithAPIKeyHandler)

    app.Get("/leaflet/:endpoint", leafletRouteWithoutAPIKeyHandler)
    app.Post("/leaflet/:endpoint", leafletRouteWithoutAPIKeyHandler)

    // admin group (routes) are protected by password
    admin_password := os.Getenv("TAXMAN_ADMIN_PASSWORD")
    if admin_password == "" {
        fmt.Printf("WARNING! Using 'yakuhito' as the admin password since 'TAXMAN_ADMIN_PASSWORD' is not set.\n")
        admin_password = "yakuhito"
    }
    admin := app.Group("/admin")
    admin.Use(basicauth.New(basicauth.Config{
        Users: map[string]string{
            "taxman":  admin_password,
        },
    }))
    admin.Get("/", monitor.New(monitor.Config{Title: "Taxman - Metrics"}))

   log.Fatalln(app.Listen(fmt.Sprintf(":%v", port)))
}
