package main

import (
    "database/sql"
    "io/ioutil"
    "net/http"
    "context"
    "errors"
    "bytes"
    "time"
    "fmt"
    "log"
    "os"

    _ "github.com/lib/pq"
    firebase "firebase.google.com/go"

    "github.com/gofiber/fiber/v2"
    "github.com/stripe/stripe-go"
    "google.golang.org/api/option"
    "github.com/stripe/stripe-go/webhook"
    "github.com/stripe/stripe-go/customer"
    "github.com/sacsand/gofiber-firebaseauth"
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

type User struct {
    uid string
    has_active_stripe_subscription bool
    stripe_user_id sql.NullString
    stripe_item_id sql.NullString
    stripe_subscription_id sql.NullString
    stripe_product_id sql.NullString
    stripe_price_id sql.NullString
}

type GiftCode struct {
    code string
    credits int64
    used bool
    uid sql.NullString
}

var db *sql.DB;

func getWeekId() string {
    // https://stackoverflow.com/questions/47193649/week-number-based-on-timestamp-with-go
    tn := time.Now().UTC()
    year, week := tn.ISOWeek()

    return fmt.Sprintf("%d-%d", year, week)
}


func getAPIKey(api_key string) *APIKey {
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

func getWeeklyUsage(api_key string) *WeeklyUsage {
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

func createWeeklyUsage(api_key string) *WeeklyUsage {
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

    return getWeeklyUsage(api_key)
}

func decreaseAPIKeyFreeUsage(api_key string, credits uint64) error {
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

func billCredits(api_key string, uid string, credits uint64) error {
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

func updateCustomerBillingDetails(
    uid string,
    has_active_stripe_subscription bool,
    stripe_user_id string,
    stripe_item_id string,
    stripe_subscription_id string,
    stripe_product_id string,
    stripe_price_id string,
) error {
    result, err := db.Exec(
        "UPDATE users SET has_active_stripe_subscription = $1," +
        " stripe_user_id = $2," +
        " stripe_item_id = $3," + 
        " stripe_subscription_id = $4," + 
        " stripe_product_id = $5," +
        " stripe_price_id = $6 " +
        "WHERE uid = $7",
        has_active_stripe_subscription, stripe_user_id, stripe_item_id, stripe_subscription_id, stripe_product_id, stripe_price_id, uid,
    )
    if err != nil {
        return err
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        return err
    }

    if rowsAffected != 1 {
        err = errors.New(uid + " -> ????? (0 or more than 1 row affected in updateCustomerBillingDetails)")
        return err
    }

    return nil
}

func updateCustomerActiveSubscription(
    stripe_user_id string,
    has_active_stripe_subscription bool,
) error {
    result, err := db.Exec(
        "UPDATE users SET has_active_stripe_subscription = $1 WHERE stripe_user_id = $2",
        has_active_stripe_subscription, stripe_user_id,
    )
    if err != nil {
        return err
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        return err
    }

    if rowsAffected > 1 {
        err = errors.New(stripe_user_id + " -> ????? (more than 1 rows affected in updateCustomerActiveSubscription)")
        return err
    }

    return nil
}

func revokeAPIKeys(
    uid string,
) error {
    _, err := db.Exec(
        "UPDATE api_keys SET disabled = true WHERE uid = $1",
        uid,
    )
    if err != nil {
        return err
    }

    return nil
}

func getWeeklyUsageForUser(uid string) (int64, error) {
    week_id := getWeekId()
    row := db.QueryRow("SELECT SUM(credits) FROM weekly_usage WHERE uid = $1 AND week = $2", uid, week_id)

    var totalWeeklyUsage int64
    err := row.Scan(&totalWeeklyUsage)
    if err == sql.ErrNoRows {
        return 0, nil
    } else if err != nil {
        log.Print(err)
        return 0, err
    }

    return totalWeeklyUsage, nil
}

func checkAPIKeyAndReturnOrigin(api_key string, endpoint string) (string /*origin*/, bool /*errored*/) {
    const CREDITS_PER_REQUEST = 420;

    apiKey := getAPIKey(api_key)
    if apiKey == nil || apiKey.disabled {
        return "", true
    }

    weeklyUsage := getWeeklyUsage(api_key)
    if weeklyUsage == nil {
        weeklyUsage = createWeeklyUsage(api_key)
        if weeklyUsage == nil {
            return "", true
        }
    }
    if apiKey.weekly_limit != 0 && weeklyUsage.credits >= apiKey.weekly_limit {
        return "", true
    }

    if apiKey.free_credits_remaining > CREDITS_PER_REQUEST {
        if err := decreaseAPIKeyFreeUsage(api_key, CREDITS_PER_REQUEST); err != nil {
            log.Print(err)
            return "", true
        }
    } else {
        billCredits(api_key, apiKey.uid, CREDITS_PER_REQUEST)
    }

    return apiKey.origin, false
} 

func leafletHandler(c *fiber.Ctx, api_key string, endpoint string, leaflet_base_url string) error {
    origin, errored := checkAPIKeyAndReturnOrigin(api_key, endpoint)
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

func leafletRouteWithAPIKeyHandler(c *fiber.Ctx, leaflet_base_url string) error {
    api_key := c.Params("api_key")
    endpoint := c.Params("endpoint")

    c.Set("X-API-Key", api_key)
    return leafletHandler(c, api_key, endpoint, leaflet_base_url)
}

func leafletRouteWithoutAPIKeyHandler(c *fiber.Ctx, leaflet_base_url string) error {
    api_key := c.Query("api-key")
    if api_key == "" {
        api_key = c.Get("X-API-Key")
    } else {
        c.Set("X-API-Key", api_key)
    }
    endpoint := c.Params("endpoint")

    return leafletHandler(c, api_key, endpoint, leaflet_base_url)
}

func stripeWebhook(c *fiber.Ctx) error {
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

    switch event.Type {
        case "customer.subscription.created":
            customerId := event.Data.Object["customer"].(string)
            subscriptionId := event.Data.Object["id"].(string)
            item := event.Data.Object["items"].(map[string]interface{})["data"].([]interface {})[0].(map[string]interface{})
            itemId := item["id"].(string)
            plan := item["plan"].(map[string]interface{})
            productId := plan["id"].(string)
            priceId := plan["product"].(string)

            customer, err := customer.Get(customerId, nil)
            if err != nil {
                log.Print(err);
                return c.Status(500).SendString("error #1 ser")
            }
            uid := customer.Metadata["uid"]
            err = updateCustomerBillingDetails(uid, true, customerId, itemId, subscriptionId, productId, priceId)
            if err != nil {
                log.Print(err);
                return c.Status(500).SendString("error #2 ser")
            }
            break;
        case "invoice.paid":
            customerId := event.Data.Object["customer"].(string)
            
            err := updateCustomerActiveSubscription(customerId, true);
            if err != nil {
                log.Print(err);
                return c.Status(500).SendString("error ser")
            }
            break;
        case "invoice.payment_failed":
            customerId := event.Data.Object["customer"].(string)
            customer, err := customer.Get(customerId, nil)
            if err != nil {
                log.Print(err);
                return c.Status(500).SendString("error #1 ser")
            }
            uid := customer.Metadata["uid"]

            err = updateCustomerActiveSubscription(customerId, false);
            if err != nil {
                log.Print(err);
                return c.Status(500).SendString("error #2 ser")
            }

            err = revokeAPIKeys(uid);
            if err != nil {
                log.Print(err);
                return c.Status(500).SendString("error #3 ser")
            }
            break;
        case "customer.subscription.deleted":
            customerId := event.Data.Object["customer"].(string)
            customer, err := customer.Get(customerId, nil)
            if err != nil {
                log.Print(err);
                return c.Status(500).SendString("error #1 ser")
            }
            uid := customer.Metadata["uid"]
            
            err = updateCustomerBillingDetails(uid, false, customerId, "", "", "", "")
            if err != nil {
                log.Print(err);
                return c.Status(500).SendString("error #2 ser")
            }

            err = revokeAPIKeys(uid);
            if err != nil {
                log.Print(err);
                return c.Status(500).SendString("error #3 ser")
            }
            break;
        default:
            return c.Status(200).SendString("wat am I supposed to do with dat?!")
    }

    return c.SendString("ok ser")
}

func handleStripeUrlAPIRequest(c *fiber.Ctx) error {
    currentUser := c.Locals("user").(gofiberfirebaseauth.User)
    fmt.Println(currentUser)
    return c.SendString(currentUser.Email)
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
        return leafletRouteWithAPIKeyHandler(c, leaflet_base_url)
    })
    app.Post("/:api_key<guid>/leaflet/:endpoint", func(c *fiber.Ctx) error {
        return leafletRouteWithAPIKeyHandler(c, leaflet_base_url)
    })

    app.Get("/leaflet/:endpoint", func(c *fiber.Ctx) error {
        return leafletRouteWithoutAPIKeyHandler(c, leaflet_base_url)
    })
    app.Post("/leaflet/:endpoint", func(c *fiber.Ctx) error {
        return leafletRouteWithoutAPIKeyHandler(c, leaflet_base_url)
    })

    // Stripe webhook
    stripe_token := os.Getenv("STRIPE_SECRET_KEY")
    if stripe_token == "" {
        fmt.Printf("STRIPE_SECRET_KEY not set - this might be very bad\n")
    } else {
        stripe.Key = stripe_token
    }
    app.Post("/stripe/webhook", stripeWebhook)

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

    // Dashboard API
    fbcreds := os.Getenv("FIREBASE_ADMIN_CREDS")
    if fbcreds == "" {
        log.Fatalf("Firebase credentials not found in FIREBASE_ADMIN_CREDS")
    }
    fbapp, err := firebase.NewApp(
        context.Background(),
        nil,
        option.WithCredentialsJSON([]byte(fbcreds)),
    )
    if err != nil {
        log.Fatalf("error initializing Firebase app: %v\n", err)
    }

    api := app.Group("/api")
    api.Use(gofiberfirebaseauth.New(gofiberfirebaseauth.Config{
        FirebaseApp:  fbapp,
        CheckEmailVerified : true,
    }))
    api.Get("/stripe-url", handleStripeUrlAPIRequest)

    // Start server
    log.Fatalln(app.Listen(fmt.Sprintf(":%v", port)))
}
