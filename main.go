package main

import (
   "database/sql"
   "errors"
   "time"
   "fmt"
   "log"
   "os"

   _ "github.com/lib/pq" // add this
   "github.com/gofiber/fiber/v2"
   "github.com/gofiber/fiber/v2/middleware/monitor"
   "github.com/gofiber/fiber/v2/middleware/basicauth"
)

type APIKey struct {
    api_key string
    disabled bool
    free_credits_remaining uint64
    weekly_limit uint64
    name string
    origin string
    uid string
}

type WeeklyUsage struct {
    id int64
    api_key string
    credits uint64
    week string
}

func getWeekId() string {
    // https://stackoverflow.com/questions/47193649/week-number-based-on-timestamp-with-go
    tn := time.Now().UTC()
    year, week := tn.ISOWeek()

    return fmt.Sprintf("%d-%d", year, week)
}


func getAPIKey(db *sql.DB, api_key string) *APIKey {
    apiKeyRow := db.QueryRow("SELECT * FROM api_keys WHERE api_key = $1", api_key)

    apiKey := new(APIKey)
    err := apiKeyRow.Scan(
        &apiKey.api_key,
        &apiKey.disabled,
        &apiKey.free_credits_remaining,
        &apiKey.weekly_limit,
        &apiKey.name,
        &apiKey.origin,
        &apiKey.uid,
    )
    if err == sql.ErrNoRows {
        return nil
    } else if err != nil {
        log.Fatal(err)
        return nil
    }

    return apiKey
}

func getWeeklyUsage(db *sql.DB, api_key string) *WeeklyUsage {
    week_id := getWeekId()
    row := db.QueryRow("SELECT * FROM weekly_usage WHERE api_key = $1 AND week = $2", api_key, week_id)

    weeklyUsage := new(WeeklyUsage)
    err := row.Scan(
        &weeklyUsage.id,
        &weeklyUsage.api_key,
        &weeklyUsage.credits,
        &weeklyUsage.week,
    )
    if err == sql.ErrNoRows {
        return nil
    } else if err != nil {
        log.Fatal(err)
        return nil
    }

    return weeklyUsage
}

func createWeeklyUsage(db *sql.DB, api_key string) *WeeklyUsage {
    week_id := getWeekId()
    result, err := db.Exec(
        // prevent race conditions
        "INSERT INTO weekly_usage(api_key, credits, week) SELECT $1, $2, $3 WHERE NOT EXISTS (SELECT 1 FROM weekly_usage WHERE api_key = $1 AND week = $3)",
        api_key, 0, week_id,
    )
    if err != nil {
        log.Fatal(err)
        return nil
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        log.Fatal(err)
        return nil
    }

    if rowsAffected > 1 {
        log.Fatal(api_key + " -> ????? (more than 1 row affected in createWeeklyUsage)")
        return nil
    }

    return getWeeklyUsage(db, api_key)
}

func decreaseAPIKeyFreeUsage(db *sql.DB, api_key string, credits uint64) error {
    result, err := db.Exec(
        "UPDATE api_keys SET free_credits_remaining = free_credits_remaining - $1 WHERE api_key = $2",
        credits, api_key,
    )
    if err != nil {
        return err
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        return err
    }

    if rowsAffected != 1 {
        err = errors.New(api_key + " -> ????? (0 or more than 1 row affected in decreaseAPIKeyFreeUsage)")
        return err
    }

    return nil
}

func billCredits(db *sql.DB, api_key string, uid string, credits uint64) error {
    week_id := getWeekId()
    result, err := db.Exec(
        "UPDATE weekly_usage SET credits = credits + $1 WHERE api_key = $2 AND week = $3",
        credits, api_key, week_id,
    )
    if err != nil {
        return err
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        return err
    }

    if rowsAffected != 1 {
        err = errors.New(api_key + " -> ????? (0 or more than 1 row affected in billCredits, #1)")
        return err
    }

    result, err = db.Exec(
        "UPDATE credits_to_bill SET credits = credits + $1 WHERE api_key = $2",
        credits, api_key,
    )
    if err != nil {
        return err
    }

    rowsAffected, err = result.RowsAffected()
    if err != nil {
        return err
    }

    if rowsAffected < 1 {
        result, err = db.Exec(
            "INSERT INTO credits_to_bill(api_key, uid, credits) VALUES($1, $2, $3)",
            api_key, uid, credits,
        )
        return err
    }

    return nil
}

func checkAPIKeyAndReturnOrigin(api_key string, endpoint string, db *sql.DB) (string /*origin*/, bool /*errored*/) {
    const CREDITS_PER_REQUEST = 420;

    apiKey := getAPIKey(db, api_key)
    if apiKey == nil || apiKey.disabled {
        return "", true
    }

    weeklyUsage := getWeeklyUsage(db, api_key)
    if weeklyUsage == nil {
        weeklyUsage = createWeeklyUsage(db, api_key)
        if weeklyUsage == nil {
            return "", true
        }
    }
    if apiKey.weekly_limit != 0 && weeklyUsage.credits >= apiKey.weekly_limit {
        return "", true
    }

    if apiKey.free_credits_remaining > CREDITS_PER_REQUEST {
        if err := decreaseAPIKeyFreeUsage(db, api_key, CREDITS_PER_REQUEST); err != nil {
            log.Fatal(err)
            return "", true
        }
    } else {
        billCredits(db, api_key, apiKey.uid, CREDITS_PER_REQUEST)
    }

    return apiKey.origin, false
} 

func leafletHandler(c *fiber.Ctx, api_key string, endpoint string, db *sql.DB) error {
    origin, errored := checkAPIKeyAndReturnOrigin(api_key, endpoint, db)
    if errored {
        return c.SendString("Taxman has blocked this request.")
    }
    c.Set("Access-Control-Allow-Origin", origin)

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
