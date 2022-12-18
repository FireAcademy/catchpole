package main

import (
    "os"
    "github.com/gofiber/fiber/v2"
    "github.com/gofiber/fiber/v2/middleware/monitor"
    "github.com/gofiber/fiber/v2/middleware/basicauth"
)

func SetupAdminRoutes(app *fiber.App) {
    // admin group (routes) are protected by password
    admin_password := os.Getenv("CATCHPOLE_ADMIN_PASSWORD")
    if admin_password == "" {
        panic("CATCHPOLE_ADMIN_PASSWORD not set, ser")
    }
    admin := app.Group("/admin")
    admin.Use(basicauth.New(basicauth.Config{
        Users: map[string]string{
            "catchpole":  admin_password,
        },
    }))
    admin.Get("/", monitor.New(monitor.Config{Title: "Catchpole - Metrics"}))
}