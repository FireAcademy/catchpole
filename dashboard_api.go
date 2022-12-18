package main

import (
    "os"
    "log"
    "context"
    "database/sql"
    _ "github.com/lib/pq"
    "github.com/gofiber/fiber/v2"
    "google.golang.org/api/option"
    firebase "firebase.google.com/go"
    "github.com/stripe/stripe-go/v74"
    "github.com/sacsand/gofiber-firebaseauth"
    "github.com/stripe/stripe-go/v74/customer"
    "github.com/stripe/stripe-go/v74/checkout/session"
    portalsession "github.com/stripe/stripe-go/v74/billingportal/session"
)

func HandleStripeUrlAPIRequest(c *fiber.Ctx, price_id string) error {
    authUser := c.Locals("user").(gofiberfirebaseauth.User)
    uid := authUser.UserID
    email := authUser.Email
    user := GetUser(uid)

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

func HandleDashboardDataAPIRequest(c *fiber.Ctx) error {
    uid := c.Locals("user").(gofiberfirebaseauth.User).UserID
    user := GetUser(uid)
    if user == nil {
        return MakeErrorResponse(c, "error ocurred while fetching user")
    }
    api_keys := getAPIKeysForUser(uid)
    if api_keys == nil {
        return MakeErrorResponse(c, "error ocurred while fetching API keys")
    }
    weekly_usages := getWeeklyUsagesForUser(uid)
    if weekly_usages == nil {
        return MakeErrorResponse(c, "error ocurred while fetching weekly usage")
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

func HandleCreateAPIKeyAPIRequest(c *fiber.Ctx) error {
    uid := c.Locals("user").(gofiberfirebaseauth.User).UserID
    user := GetUser(uid)
    if user == nil {
        return MakeErrorResponse(c, "error ocurred while fetching user")
    }

    args := new(CreateAPIKeyArgs)

    if err := c.BodyParser(args); err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "error ocurred while decoding input data")
    }

    // check args.WeeklyCreditLimit
    if args.WeeklyCreditLimit < 0 || args.WeeklyCreditLimit > 4200000 * 313337 {
        return MakeErrorResponse(c, "that's a funny-looking credit limit")
    }

    // check args.Name
    if len(args.Name) < 4 || len(args.Name) > 32 {
        return MakeErrorResponse(c, "name should be 4-32 chars long")
    }
    
    // check args.Origin
    if len(args.Origin) < 1 || len(args.Origin) > 128 {
        return MakeErrorResponse(c, "origin should be 1-128 chars long")
    }

    var freeUsage int64
    freeUsage = 0
    if !user.received_free_credits {
        freeUsage = 4200000
        err := UpdateUserReceivedFreeCredits(uid, true)
        if err != nil {
            return MakeErrorResponse(c, "error ocurred while doing stuff")
        }
    }

    if !user.has_active_stripe_subscription && args.WeeklyCreditLimit > freeUsage {
        return MakeErrorResponse(c, "weekly credit limit can be free usage at most unless you subscribe to our service")
    }

    err := CreateAPIKey(uid, freeUsage, args.WeeklyCreditLimit, args.Name, args.Origin)
    if err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "error ocurred while creating API key")
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

func HandleUpdateAPIKeyAPIRequest(c *fiber.Ctx) error {
    uid := c.Locals("user").(gofiberfirebaseauth.User).UserID
    user := GetUser(uid)
    if user == nil {
        return MakeErrorResponse(c, "error ocurred while fetching user")
    }

    args := new(UpdateAPIKeyArgs)

    if err := c.BodyParser(args); err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "error ocurred while decoding input data")
    }

    // check args.ApiKey
    if len(args.ApiKey) != 36 {
        return MakeErrorResponse(c, "invalid API key provided")
    }
    currentApiKey := getAPIKey(args.ApiKey)
    if currentApiKey == nil || currentApiKey.uid != uid {
        return MakeErrorResponse(c, "invalid API key provided")
    }

    // check args.WeeklyCreditLimit
    if args.WeeklyCreditLimit < 0 || args.WeeklyCreditLimit > 4200000 * 313337 {
        return MakeErrorResponse(c, "that's a funny-looking credit limit")
    }

    // check args.Name
    if len(args.Name) < 4 || len(args.Name) > 32 {
        return MakeErrorResponse(c, "name should be 4-32 chars long")
    }
    
    // check args.Origin
    if len(args.Origin) < 1 || len(args.Origin) > 128 {
        return MakeErrorResponse(c, "origin should be 1-128 chars long")
    }

    // check args.WeeklyCreditLimit
    if !user.has_active_stripe_subscription && args.WeeklyCreditLimit > currentApiKey.free_credits_remaining {
        return MakeErrorResponse(c, "weekly credit limit can be free usage at most unless you subscribe to our service")
    }

    // check args.Disabled
    // no checks required!

    err := updateAPIKey(args.ApiKey, args.Disabled, args.WeeklyCreditLimit, args.Name, args.Origin)
    if err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "error ocurred while updating API key")
    }

    return c.JSON(fiber.Map{
        "success": true,
    });
}

type GenerateGiftCodesArgs struct {
    Count int `json:"count"`
    Credits int64 `json:"credits"`
}

func HandleGenerateGiftCodesAPIRequest(c *fiber.Ctx) error {
    user := c.Locals("user").(gofiberfirebaseauth.User)
    email := user.Email

    if email != "y@kuhi.to" {
        return MakeErrorResponse(c, "only yakuhito can access this endpoint!")
    }

    args := new(GenerateGiftCodesArgs)

    if err := c.BodyParser(args); err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "error ocurred while decoding input data")
    }

    if args.Count < 1 || args.Count > 10000 {
        return MakeErrorResponse(c, "count should be in [1, 10000]")
    }

    // don't check credits it's admin that's making the request
    gift_codes := make([]string, 0)
    for i := 0; i < args.Count; i++ {
        gift_code, err := GenerateGiftCode(args.Credits)
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

func HandleUseGiftCodeAPIRequest(c *fiber.Ctx) error {
    uid := c.Locals("user").(gofiberfirebaseauth.User).UserID
    user := GetUser(uid)
    if user == nil {
        return MakeErrorResponse(c, "error ocurred while fetching user")
    }

    giftCodeAttempts := GetGiftCodeAttempts(uid)
    if giftCodeAttempts.fails >= 42 {
        return MakeErrorResponse(c, "You've been blocked after claiming invalid gift codes for too many times. Contact the admin to be unhammered.")
    }

    args := new(UseGiftCodeArgs)

    if err := c.BodyParser(args); err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "error ocurred while decoding input data")
    }

    if len(args.Code) != 36 || len(args.APIKey) != 36 {
        return MakeErrorResponse(c, "incorrect input")
    }

    apiKey := GetAPIKey(args.APIKey)
    if apiKey == nil || apiKey.uid != uid {
        return MakeErrorResponse(c, "unknown API key")
    }

    giftCode := GetGiftCode(args.Code)
    if giftCode == nil || giftCode.used {
        increaseGiftCodeAttempts(uid)
        return MakeErrorResponse(c, "invalid gift code")
    }

    err := MarkGiftCodeAsUsed(args.Code, uid)
    if err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "error while processing gift code")
    }

    err = IncreaseAPIKeyFreeUsage(args.APIKey, giftCode.credits)
    if err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "error while updating API key free credits")
    }
    
    return c.JSON(fiber.Map{"success": true})
}

func SetupDashboardAPIRoutes(app *fiber.App) {
    fbcreds := os.Getenv("FIREBASE_ADMIN_CREDS")
    if fbcreds == "" {
        panic("Firebase credentials not found in FIREBASE_ADMIN_CREDS")
    }
    fbapp, err := firebase.NewApp(
        context.Background(),
        nil,
        option.WithCredentialsJSON([]byte(fbcreds)),
    )
    if err != nil {
        panic("error initializing Firebase app: %v\n", err)
    }

    api := app.Group("/api")
    stripe_price_id := os.Getenv("STRIPE_PRICE_ID")
    if stripe_price_id == "" {
        panic("STRIPE_PRICE_ID environment variable not set; exiting...")
    }
    api.Use(gofiberfirebaseauth.New(gofiberfirebaseauth.Config{
        FirebaseApp:  fbapp,
        CheckEmailVerified : true,
    }))
    api.Get("/stripe-url", func (c *fiber.Ctx) error {
        return HandleStripeUrlAPIRequest(c, stripe_price_id);
    })
    api.Get("/dashboard-data", HandleDashboardDataAPIRequest)
    api.Post("/api-key", HandleCreateAPIKeyAPIRequest)
    api.Put("/api-key", HandleUpdateAPIKeyAPIRequest)
    api.Post("/generate-gift-codes", HandleGenerateGiftCodesAPIRequest)
    api.Post("/gift-code", HandleUseGiftCodeAPIRequest)
}