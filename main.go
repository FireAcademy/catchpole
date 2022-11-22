package main

import (
    "database/sql"
    "io/ioutil"
    "net/http"
    "errors"
    "bytes"
    "time"
    "fmt"
    "log"
    "os"

    _ "github.com/lib/pq" // add this
    "github.com/gofiber/fiber/v2"
    "github.com/stripe/stripe-go"
    "github.com/stripe/stripe-go/webhook"
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
        log.Print(err)
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
        log.Print(err)
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
        log.Print(err)
        return nil
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        log.Print(err)
        return nil
    }

    if rowsAffected > 1 {
        log.Print(api_key + " -> ????? (more than 1 row affected in createWeeklyUsage)")
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
            log.Print(err)
            return "", true
        }
    } else {
        billCredits(db, api_key, apiKey.uid, CREDITS_PER_REQUEST)
    }

    return apiKey.origin, false
} 

func leafletHandler(c *fiber.Ctx, api_key string, endpoint string, db *sql.DB, leaflet_base_url string) error {
    origin, errored := checkAPIKeyAndReturnOrigin(api_key, endpoint, db)
    if errored {
        return c.SendString("Taxman has blocked this request.")
    }
    c.Set("Access-Control-Allow-Origin", origin)

    url := fmt.Sprintf("%s/%s", leaflet_base_url, endpoint)
    resp, err := http.Post(url, "application/json", bytes.NewBuffer(c.Body()))
    if err != nil {
        log.Print(err)
        return c.SendString("Leaflet: error ocurred when processing request")
    }
    defer resp.Body.Close()
    
    body, err := ioutil.ReadAll(resp.Body)
    if err != nil {
        log.Print(err)
        return c.SendString("Leaflet: error ocurred when reading response")
    }
    return c.SendString(string(body))
}

func leafletRouteWithAPIKeyHandler(c *fiber.Ctx, db *sql.DB, leaflet_base_url string) error {
    api_key := c.Params("api_key")
    endpoint := c.Params("endpoint")

    c.Set("X-API-Key", api_key)
    return leafletHandler(c, api_key, endpoint, db, leaflet_base_url)
}

func leafletRouteWithoutAPIKeyHandler(c *fiber.Ctx, db *sql.DB, leaflet_base_url string) error {
    api_key := c.Query("api-key")
    if api_key == "" {
        api_key = c.Get("X-API-Key")
    } else {
        c.Set("X-API-Key", api_key)
    }
    endpoint := c.Params("endpoint")

    return leafletHandler(c, api_key, endpoint, db, leaflet_base_url)
}

func stripeWebhook(c *fiber.Ctx, db *sql.DB) error {
    stripe_webhook_secret := os.Getenv("STRIPE_WEBHOOK_SECRET")
    if stripe_webhook_secret == "" {
        fmt.Printf("STRIPE_WEBHOOK_SECRET not specified - this is BAD!")
        return c.Status(500).SendString("not ok ser")
    }

    event, err := webhook.ConstructEvent(c.Body(), c.Get("Stripe-Signature"), stripe_webhook_secret)
    if err != nil {
        log.Print(err)
        return c.Status(400).SendString("not ok ser")
    }

    // get inspiration from:
    // https://github.com/FireAcademy/fireacademy-firebase/blob/master/functions/src/stripe.ts
    switch event.Type {
        case "checkout.session.completed":
            // someone completed a checkout session!
            // there's a new subscriber 
        case "invoice.paid":
            // someone paid their invoice
            // mark them as a paying customer if they were not
        case "invoice.payment_failed":
            // someone failed to pay the money they own us
            // diasble their keys, then call in the mob
        case "customer.subscription.deleted":
            // someone deleted teir subscription :(
            // time to mark them as a non-customer
        default:
            return c.Status(200).SendString("wat am I supposed to do with dat?!")
    }


    fmt.Printf("%s\n", event.Type)
    return c.SendString("ok ser")
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
   leaflet_base_url := fmt.Sprintf("http://%s:%s", leaflet_host, leaflet_port)
   fmt.Printf("Leaflet at %s\n", leaflet_base_url)


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
        panic(err)
    }
    defer db.Close()

    err = db.Ping()
    if err != nil {
        panic(err)
    }

    // Leaflet
    app.Get("/:api_key<guid>/leaflet/:endpoint", func(c *fiber.Ctx) error {
        return leafletRouteWithAPIKeyHandler(c, db, leaflet_base_url)
    })
    app.Post("/:api_key<guid>/leaflet/:endpoint", func(c *fiber.Ctx) error {
        return leafletRouteWithAPIKeyHandler(c, db, leaflet_base_url)
    })

    app.Get("/leaflet/:endpoint", func(c *fiber.Ctx) error {
        return leafletRouteWithoutAPIKeyHandler(c, db, leaflet_base_url)
    })
    app.Post("/leaflet/:endpoint", func(c *fiber.Ctx) error {
        return leafletRouteWithoutAPIKeyHandler(c, db, leaflet_base_url)
    })

    // Stripe webhook
    stripe_token := os.Getenv("STRIPE_SECRET_KEY")
    if stripe_token == "" {
        fmt.Printf("STRIPE_SECRET_KEY not set - this might be very bad\n")
    } else {
        stripe.Key = stripe_token
    }
    app.Post("/stripe/webhook", func(c *fiber.Ctx) error {
        return stripeWebhook(c, db)
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
