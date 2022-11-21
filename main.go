package main

import (
   "database/sql"
   "fmt"
   "log"
   "os"

   _ "github.com/lib/pq" // add this
   "github.com/gofiber/fiber/v2"
   "github.com/gofiber/fiber/v2/middleware/monitor"
   "github.com/gofiber/fiber/v2/middleware/basicauth"
)

func leafletHandler(c *fiber.Ctx, api_key string, endpoint string, db *sql.DB) error {
    rows, err := db.Query("SELECT COUNT(*) FROM api_keys")
    if err != nil {
        log.Fatal(err)
        return c.SendString("db error :(")
    }
    defer rows.Close()

    var count int

    for rows.Next() {   
        if err := rows.Scan(&count); err != nil {
            log.Fatal(err)
            return c.SendString("db error :(")
        }
    }

    return c.SendString(fmt.Sprintf("%s-%s-%d", api_key, endpoint, count))
}

func leafletRouteWithAPIKeyHandler(c *fiber.Ctx, db *sql.DB) error {
    api_key := c.Params("api_key")
    endpoint := c.Params("endpoint")

    c.Set("X-API-Key", api_key)
    return leafletHandler(c, api_key, endpoint, db)
}

func leafletRouteWithoutAPIKeyHandler(c *fiber.Ctx, db *sql.DB) error {
    api_key := c.Query("api-key")
    if api_key == "" {
        api_key = c.Get("X-API-Key")
    } else {
        c.Set("X-API-Key", api_key)
    }
    endpoint := c.Params("endpoint")

    return leafletHandler(c, api_key, endpoint, db)
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


    // Index
    app.Get("/", func(c *fiber.Ctx) error {
        return c.SendString("Taxman is alive and well.")
    })

    // DB
    db_conn_string := os.Getenv("DB_CONN_STRING")
    if db_conn_string == "" {
        fmt.Printf("DB_CONN_STRING not specified, exiting :(\n")
        return
    }
    db, err := sql.Open("postgres", db_conn_string)
    if err != nil {
        log.Fatal(err)
    }

    // Leaflet
    app.Get("/:api_key<guid>/leaflet/:endpoint", func(c *fiber.Ctx) error {
        return leafletRouteWithAPIKeyHandler(c, db)
    })
    app.Post("/:api_key<guid>/leaflet/:endpoint", func(c *fiber.Ctx) error {
        return leafletRouteWithAPIKeyHandler(c, db)
    })

    app.Get("/leaflet/:endpoint", func(c *fiber.Ctx) error {
        return leafletRouteWithoutAPIKeyHandler(c, db)
    })
    app.Post("/leaflet/:endpoint", func(c *fiber.Ctx) error {
        return leafletRouteWithoutAPIKeyHandler(c, db)
    })

    // Metrics
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


    // Start server
    log.Fatalln(app.Listen(fmt.Sprintf(":%v", port)))
}
