package main

import (
    "os"
    "fmt"
    "log"
    "github.com/gofiber/fiber/v2"
    "github.com/gofiber/fiber/v2/middleware/cors"
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

    app.Use(cors.New())
    app.Get("/", func(c *fiber.Ctx) error {
        return c.SendString("Catchpole is alive and well.")
    })

    SetupDB()

    SetupLeafletRoutes(app)
    SetupStripeWebhook(app)
    SetupAdminRoutes(app)
    SetupDashboardAPIRoutes(app)
    SetupBetaRoutes(app)

    go StripeBillRoutine()

    log.Fatalln(app.Listen(fmt.Sprintf(":%v", port)))
}
