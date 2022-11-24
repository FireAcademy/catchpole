package main

import (
    "os"
    "fmt"
    "log"
    "time"
    "bytes"
    "context"
    "net/http"
    "io/ioutil"
    "database/sql"
    _ "github.com/lib/pq"
    "github.com/gofiber/fiber/v2"
    "google.golang.org/api/option"
    firebase "firebase.google.com/go"
    "github.com/stripe/stripe-go/v74"
    "github.com/stripe/stripe-go/v74/webhook"
    "github.com/sacsand/gofiber-firebaseauth"
    "github.com/stripe/stripe-go/v74/customer"
    "github.com/stripe/stripe-go/v74/usagerecord"
    "github.com/stripe/stripe-go/v74/subscription"
    "github.com/gofiber/fiber/v2/middleware/monitor"
    "github.com/gofiber/fiber/v2/middleware/basicauth"
    "github.com/stripe/stripe-go/v74/checkout/session"
    portalsession "github.com/stripe/stripe-go/v74/billingportal/session"
)

var leaflet_base_url string

func taxTrafficAndReturnOrigin(api_key string, credits_per_request int64) (string /*origin*/, bool /*errored*/) {
    apiKey, weeklyUsage := getAPIKeyAndWeeklyUsage(api_key)

    if weeklyUsage == nil {
        weeklyUsage = createWeeklyUsage(api_key)
        apiKey = getAPIKey(api_key)
        if weeklyUsage == nil {
            return "", true
        }
    }
    if apiKey == nil || apiKey.disabled {
        return "", true
    }

    if apiKey.weekly_credit_limit != -1 && weeklyUsage.credits >= apiKey.weekly_credit_limit {
        return "", true
    }

    err := increaseWeeklyUsage(api_key, credits_per_request)
    if err != nil {
        return "", true
    }
    if apiKey.free_credits_remaining > credits_per_request {
        if err := decreaseAPIKeyFreeUsage(api_key, credits_per_request); err != nil {
            log.Print(err)
            return "", true
        }
    } else {
        billCredits(api_key, apiKey.uid, credits_per_request)
    }

    return apiKey.origin, false
} 

func getAPIKeyForRequest(c *fiber.Ctx) string {
    api_key := c.Params("api_key")
    if api_key == "" {
        api_key = c.Get("X-API-Key")
    }
    if api_key == "" {
        api_key = c.Query("api-key")
    }

    return api_key
}

func leafletHandler(c *fiber.Ctx) error {
    api_key := getAPIKeyForRequest(c)
    if api_key == "" {
        return c.Status(401).SendString("No API key provided.")
    }    

    const CREDITS_PER_REQUEST = 420
    origin, errored := taxTrafficAndReturnOrigin(api_key, CREDITS_PER_REQUEST)
    if errored {
        return c.Status(401).SendString("Catchpole has blocked this request.")
    }
    c.Set("Access-Control-Allow-Origin", origin)

    endpoint := c.Params("endpoint")
    url := fmt.Sprintf("%s/%s", leaflet_base_url, endpoint)

    resp, err := http.Post(url, "application/json", bytes.NewBuffer(c.Body()))
    if err != nil {
        log.Print(err)
        return c.Status(500).SendString("Leaflet: error ocurred when processing request")
    }
    defer resp.Body.Close()
    
    body, err := ioutil.ReadAll(resp.Body)
    if err != nil {
        log.Print(err)
        return c.Status(500).SendString("Leaflet: error ocurred when reading response")
    }
    c.Set("Content-Type", "application/json")
    return c.SendString(string(body))
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
        case "checkout.session.completed":
            if event.Data.Object["mode"].(string) != "subscription" {
                log.Print("skipping checkout.session.completed since mode is not 'subscription'")
                log.Print("mode is: " + event.Data.Object["mode"].(string))
                log.Print(event)
                break
            }
            customerId := event.Data.Object["customer"].(string)
            subscriptionId := event.Data.Object["subscription"].(string)
            s, _ := subscription.Get(subscriptionId, nil)

            item := s.Items.Data[0]
            itemId := item.ID
            productId := item.Plan.ID
            priceId := item.Plan.Product.ID

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

func handleStripeUrlAPIRequest(c *fiber.Ctx, price_id string) error {
    authUser := c.Locals("user").(gofiberfirebaseauth.User)
    uid := authUser.UserID
    email := authUser.Email
    user := getUser(uid)

    shouldCheckOut := !user.stripe_subscription_id.Valid || user.stripe_subscription_id.String == "";

    if shouldCheckOut {
        shouldCreateCustomer := !user.stripe_user_id.Valid || user.stripe_user_id.String == "";

        if(shouldCreateCustomer) {
            params := &stripe.CustomerParams{
                Description: stripe.String("An awesome customer🔥"),
                Email: stripe.String(email),
            }
            params.AddMetadata("uid", uid)
            c, _ := customer.New(params)

            err := updateUserStripeId(uid, c.ID)
            if err != nil {
                return err
            }

            user.stripe_user_id = sql.NullString{String: c.ID, Valid: true}
        }

        params := &stripe.CheckoutSessionParams{
            Mode: stripe.String(string(stripe.CheckoutSessionModeSubscription)),
            SuccessURL: stripe.String("https://dashboard.fireacademy.io/success"),
            CancelURL: stripe.String("https://dashboard.fireacademy.io/"),
            Customer: stripe.String(user.stripe_user_id.String),
        }
        params.LineItems = []*stripe.CheckoutSessionLineItemParams{
            &stripe.CheckoutSessionLineItemParams{
                Price: stripe.String(price_id),
                Quantity: stripe.Int64(1),
            },
        }
        s, _ := session.New(params)

        return c.JSON(fiber.Map{"url": s.URL})
    }

    params := &stripe.BillingPortalSessionParams{
        Customer:  stripe.String(user.stripe_user_id.String),
        ReturnURL: stripe.String("https://dashboard.fireacademy.io/"),
    }
    ps, _ := portalsession.New(params)


    return c.JSON(fiber.Map{"url": ps.URL})
}

func handleDashboardDataAPIRequest(c *fiber.Ctx) error {
    uid := c.Locals("user").(gofiberfirebaseauth.User).UserID
    user := getUser(uid)
    if user == nil {
        return c.Status(500).JSON(fiber.Map{"message": "error ocurred while fetching user"})
    }
    api_keys := getAPIKeysForUser(uid)
    if api_keys == nil {
        return c.Status(500).JSON(fiber.Map{"message": "error ocurred while fetching API keys"})
    }
    weekly_usages := getWeeklyUsagesForUser(uid)
    if weekly_usages == nil {
        return c.Status(500).JSON(fiber.Map{"message": "error ocurred while fetching weekly usage"})
    }

    usages := make(map[string]int64)
    for _, weekly_usage := range weekly_usages {
        usages[weekly_usage.api_key] = weekly_usage.credits
    }

    var api_keys_populated []interface{}
    for _, api_key := range api_keys {
        api_keys_populated = append(api_keys_populated, fiber.Map{
            "api_key": api_key.api_key,
            "disabled": api_key.disabled,
            "free_credits_remaining": api_key.free_credits_remaining,
            "weekly_credit_limit": api_key.weekly_credit_limit,
            "name": api_key.name,
            "origin": api_key.origin,
            "credits_used_this_week": usages[api_key.api_key],
        })
    }
    
    return c.JSON(fiber.Map{
        "user": fiber.Map{
            "uid": uid,
            "received_free_credits": user.received_free_credits,
            "has_active_stripe_subscription": user.has_active_stripe_subscription,
        },
        "api_keys": api_keys_populated,
    });
}

// Field names should start with an uppercase letter
type CreateAPIKeyArgs struct {
    WeeklyCreditLimit int64 `json:"weekly_credit_limit"`
    Name string `json:"name"`
    Origin string `json:"origin"`
}

func handleCreateAPIKeyAPIRequest(c *fiber.Ctx) error {
    uid := c.Locals("user").(gofiberfirebaseauth.User).UserID
    user := getUser(uid)
    if user == nil {
        return c.Status(500).JSON(fiber.Map{"message": "error ocurred while fetching user"})
    }

    args := new(CreateAPIKeyArgs)

    if err := c.BodyParser(args); err != nil {
        log.Print(err)
        return c.Status(500).JSON(fiber.Map{"message": "error ocurred while decoding input data"})
    }

    // check args.WeeklyCreditLimit
    if args.WeeklyCreditLimit < -1 || args.WeeklyCreditLimit > 4200000 * 313337 {
        return c.Status(500).JSON(fiber.Map{"message": "that's a funny-looking credit limit"})
    }

    // check args.Name
    if len(args.Name) < 4 || len(args.Name) > 32 {
        return c.Status(500).JSON(fiber.Map{"message": "name should be 4-32 chars long"})
    }
    
    // check args.Origin
    if len(args.Origin) < 1 || len(args.Origin) > 128 {
        return c.Status(500).JSON(fiber.Map{"message": "origin should be 1-128 chars long"})
    }

    var freeUsage int64
    freeUsage = 0
    if !user.received_free_credits {
        freeUsage = 4200000
        err := updateUserReceivedFreeCredits(uid, true)
        if err != nil {
            return c.Status(500).JSON(fiber.Map{"message": "error ocurred while doing stuff"})
        }
    }

    if !user.has_active_stripe_subscription && args.WeeklyCreditLimit > freeUsage {
        return c.Status(500).JSON(fiber.Map{"message": "weekly credit limit can be free usage at most unless you subscribe to our service"})
    }

    err := createAPIKey(uid, freeUsage, args.WeeklyCreditLimit, args.Name, args.Origin)
    if err != nil {
        log.Print(err)
        return c.Status(500).JSON(fiber.Map{"message": "error ocurred while creating API key"})
    }


    return c.JSON(fiber.Map{
        "success": true,
    });
}

type UpdateAPIKeyArgs struct {
    ApiKey string `json:"api_key"`
    Disabled bool `json:"disabled"`
    WeeklyCreditLimit int64 `json:"weekly_credit_limit"`
    Name string `json:"name"`
    Origin string `json:"origin"`
}

func handleUpdateAPIKeyAPIRequest(c *fiber.Ctx) error {
    uid := c.Locals("user").(gofiberfirebaseauth.User).UserID
    user := getUser(uid)
    if user == nil {
        return c.Status(500).JSON(fiber.Map{"message": "error ocurred while fetching user"})
    }

    args := new(UpdateAPIKeyArgs)

    if err := c.BodyParser(args); err != nil {
        log.Print(err)
        return c.Status(500).JSON(fiber.Map{"message": "error ocurred while decoding input data"})
    }

    // check args.ApiKey
    if len(args.ApiKey) != 36 {
        return c.Status(500).JSON(fiber.Map{"message": "invalid API key provided"})
    }
    currentApiKey := getAPIKey(args.ApiKey)
    if currentApiKey == nil || currentApiKey.uid != uid {
        return c.Status(500).JSON(fiber.Map{"message": "invalid API key provided"})
    }

    // check args.WeeklyCreditLimit
    if args.WeeklyCreditLimit < -1 || args.WeeklyCreditLimit > 4200000 * 313337 {
        return c.Status(500).JSON(fiber.Map{"message": "that's a funny-looking credit limit"})
    }

    // check args.Name
    if len(args.Name) < 4 || len(args.Name) > 32 {
        return c.Status(500).JSON(fiber.Map{"message": "name should be 4-32 chars long"})
    }
    
    // check args.Origin
    if len(args.Origin) < 1 || len(args.Origin) > 128 {
        return c.Status(500).JSON(fiber.Map{"message": "origin should be 1-128 chars long"})
    }

    // check args.WeeklyCreditLimit
    if !user.has_active_stripe_subscription && args.WeeklyCreditLimit > currentApiKey.free_credits_remaining {
        return c.Status(500).JSON(fiber.Map{"message": "weekly credit limit can be free usage at most unless you subscribe to our service"})
    }

    // check args.Disabled
    // no checks required!

    err := updateAPIKey(args.ApiKey, args.Disabled, args.WeeklyCreditLimit, args.Name, args.Origin)
    if err != nil {
        log.Print(err)
        return c.Status(500).JSON(fiber.Map{"message": "error ocurred while updating API key"})
    }

    return c.JSON(fiber.Map{
        "success": true,
    });
}

type GenerateGiftCodesArgs struct {
    Count int `json:"count"`
    Credits int64 `json:"credits"`
}

func handleGenerateGiftCodesAPIRequest(c *fiber.Ctx) error {
    user := c.Locals("user").(gofiberfirebaseauth.User)
    email := user.Email

    if email != "y@kuhi.to" {
        return c.Status(500).JSON(fiber.Map{"message": "only yakuhito can access this endpoint!"})
    }

    args := new(GenerateGiftCodesArgs)

    if err := c.BodyParser(args); err != nil {
        log.Print(err)
        return c.Status(500).JSON(fiber.Map{"message": "error ocurred while decoding input data"})
    }

    if args.Count < 1 || args.Count > 10000 {
        return c.Status(500).JSON(fiber.Map{"message": "count should be in [1, 10000]"})
    }

    // don't check credits it's admin that's making the request
    gift_codes := make([]string, 0)
    for i := 0; i < args.Count; i++ {
        gift_code, err := generateGiftCode(args.Credits)
        if err != nil {
            log.Print(err)
            return c.Status(500).JSON(fiber.Map{"message": "error while generating code", "error": err})
        }
        gift_codes = append(gift_codes, gift_code)
    }

    return c.JSON(fiber.Map{
        "success": true,
        "gift_codes": gift_codes,
    });
}

type UseGiftCodeArgs struct {
    Code string `json:"code"`
    APIKey string `json:"api_key"`
}

func handleUseGiftCodeAPIRequest(c *fiber.Ctx) error {
    uid := c.Locals("user").(gofiberfirebaseauth.User).UserID
    user := getUser(uid)
    if user == nil {
        return c.Status(500).JSON(fiber.Map{"message": "error ocurred while fetching user"})
    }

    giftCodeAttempts := getGiftCodeAttempts(uid)
    if giftCodeAttempts.fails >= 42 {
        return c.Status(500).JSON(fiber.Map{"message": "You've been blocked after claiming invalid gift codes for too many times. Contact the admin to be unhammered."})
    }

    args := new(UseGiftCodeArgs)

    if err := c.BodyParser(args); err != nil {
        log.Print(err)
        return c.Status(500).JSON(fiber.Map{"message": "error ocurred while decoding input data"})
    }

    if len(args.Code) != 36 || len(args.APIKey) != 36 {
        return c.Status(500).JSON(fiber.Map{"message": "incorrect input"})
    }

    apiKey := getAPIKey(args.APIKey)
    if apiKey == nil || apiKey.uid != uid {
        return c.Status(500).JSON(fiber.Map{"message": "unknown API key"})
    }

    giftCode := getGiftCode(args.Code)
    if giftCode == nil || giftCode.used {
        increaseGiftCodeAttempts(uid);
        return c.Status(500).JSON(fiber.Map{"message": "invalid gift code"})
    }

    err := markGiftCodeAsUsed(args.Code, uid)
    if err != nil {
        log.Print(err)
        return c.Status(500).JSON(fiber.Map{"message": "error while processing gift code"})
    }

    err = increaseAPIKeyFreeUsage(args.APIKey, giftCode.credits)
    if err != nil {
        log.Print(err)
        return c.Status(500).JSON(fiber.Map{"message": "error while updating API key free credits"})
    }
    
    return c.JSON(fiber.Map{"success": true})
}

func billEveryone() {
    itemId, credits := getUserBillingInfo()
    for itemId != "" {
        params := &stripe.UsageRecordParams{
            SubscriptionItem: stripe.String(itemId),
            Quantity: stripe.Int64(credits),
            Timestamp: stripe.Int64(time.Now().Unix()),
            Action: stripe.String(string(stripe.UsageRecordActionSet)),
        }

        usagerecord.New(params)

        itemId, credits = getUserBillingInfo()
    }
}

func stripeBillRoutine() {
    time.Sleep(10 * time.Second) // allow everything to boot up
    for true {
        billEveryone()
        time.Sleep(5 * time.Minute)
    }
}

func getPort() string {
    port := os.Getenv("CATCHPOLE_PORT")
   if port == "" {
       port = "5000"
   }

   return port
}

func setupLeafletBaseUrl() {
    leaflet_host := os.Getenv("LEAFLET_HOST")
    if leaflet_host == "" {
        leaflet_host = "leaflet"
    }
    leaflet_port := os.Getenv("LEAFLET_PORT")
    if leaflet_port == "" {
        leaflet_port = "18444"
    }
    leaflet_base_url = fmt.Sprintf("http://%s:%s", leaflet_host, leaflet_port)
    fmt.Printf("Leaflet at %s\n", leaflet_base_url)
}

func setupLeafletRoutes(app *fiber.App) {
    app.Get("/leaflet/:endpoint", leafletHandler)
    app.Post("/leaflet/:endpoint", leafletHandler)
    app.Get("/:api_key<guid>/leaflet/:endpoint", leafletHandler)
    app.Post("/:api_key<guid>/leaflet/:endpoint", leafletHandler)
}

func setupStripeWebhook(app *fiber.App) {
    stripe_token := os.Getenv("STRIPE_SECRET_KEY")
    if stripe_token == "" {
        fmt.Printf("STRIPE_SECRET_KEY not set - this might be very bad\n")
    } else {
        stripe.Key = stripe_token
    }
    app.Post("/stripe/webhook", stripeWebhook)
}

func setupAdminRoutes(app *fiber.App) {
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

func setupDashboardAPIRoutes(app *fiber.App) {
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
    stripe_price_id := os.Getenv("STRIPE_PRICE_ID")
    if stripe_price_id == "" {
        log.Fatalf("STRIPE_PRICE_ID environment variable not set; exiting...")
    }
    api.Use(gofiberfirebaseauth.New(gofiberfirebaseauth.Config{
        FirebaseApp:  fbapp,
        CheckEmailVerified : true,
    }))
    api.Get("/stripe-url", func (c *fiber.Ctx) error {
        return handleStripeUrlAPIRequest(c, stripe_price_id);
    })
    api.Get("/dashboard-data", handleDashboardDataAPIRequest)
    api.Post("/api-key", handleCreateAPIKeyAPIRequest)
    api.Put("/api-key", handleUpdateAPIKeyAPIRequest)
    api.Post("/generate-gift-codes", handleGenerateGiftCodesAPIRequest)
    api.Post("/gift-code", handleUseGiftCodeAPIRequest)
}

func main() {
    app := fiber.New()
    port := getPort()

    app.Get("/", func(c *fiber.Ctx) error {
        return c.SendString("Catchpole is alive and well.")
    })

    setupDB()
    setupLeafletBaseUrl()

    setupLeafletRoutes(app)
    setupStripeWebhook(app)
    setupAdminRoutes(app)
    setupDashboardAPIRoutes(app)

    go stripeBillRoutine()

    log.Fatalln(app.Listen(fmt.Sprintf(":%v", port)))
}
