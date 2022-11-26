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
       port = "5000"
   }

   return port
}

func main() {
    app := fiber.New()
    port := getPort()

    app.Get("/", func(c *fiber.Ctx) error {
        return c.SendString("Catchpole is alive and well.")
    })

    setupDB()

    setupLeafletRoutes(app)
    setupStripeWebhook(app)
    setupAdminRoutes(app)
    setupDashboardAPIRoutes(app)

    go stripeBillRoutine()

    log.Fatalln(app.Listen(fmt.Sprintf(":%v", port)))
}
