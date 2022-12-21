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
    "firebase.google.com/go/auth"
    "github.com/stripe/stripe-go/v74"
    "github.com/sacsand/gofiber-firebaseauth"
    "github.com/stripe/stripe-go/v74/customer"
    "github.com/stripe/stripe-go/v74/checkout/session"
    portalsession "github.com/stripe/stripe-go/v74/billingportal/session"
)

const ADMIN_EMAIL = "y@kuhi.to"

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

            err := UpdateUserStripeId(uid, c.ID)
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
    api_keys := GetAPIKeysForUser(uid)
    if api_keys == nil {
        return MakeErrorResponse(c, "error ocurred while fetching API keys")
    }
    weekly_usages := GetWeeklyUsagesForUser(uid)
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
    currentApiKey := GetAPIKey(args.ApiKey)
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

    err := UpdateAPIKey(args.ApiKey, args.Disabled, args.WeeklyCreditLimit, args.Name, args.Origin)
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

    if email != ADMIN_EMAIL {
        return MakeErrorResponse(c, "only the admin can access this endpoint!")
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
        IncreaseGiftCodeAttempts(uid)
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

type CreateFeedbackArgs struct {
    Message string `json:"message"`
    EmotionalState string `json:"emotional_state"`
    Anonymous bool `json:"anonymous"`
    Contact string `json:"contact`
}

func HandleCreateFeedbackAPIRequest(c *fiber.Ctx) error {
    uid := c.Locals("user").(gofiberfirebaseauth.User).UserID

    args := new(CreateFeedbackArgs)
    if err := c.BodyParser(args); err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "error ocurred while decoding input data")
    }

    if len(args.Message) < 2 {
        return MakeErrorResponse(c, "Can you please add more details to your message?")
    }

    if len(args.Message) > 2048 {
        return MakeErrorResponse(c, "You feedback looks like it should be an entire meeting.")
    }

    if len(args.EmotionalState) < 2 {
        return MakeErrorResponse(c, "Maybe you feel that way, but please try to describe it in more than 2 characters.")
    }

    if len(args.EmotionalState) > 128 {
        return MakeErrorResponse(c, "Maybe you feel that way, but please try to be more concise and describe it in 128 chars ar most.")
    }

    if !args.Anonymous && (len(args.Contact) < 4) {
        return MakeErrorResponse(c, "Your contact details are too concise. Can you add more details, please?")
    }

    if !args.Anonymous && (len(args.Contact) > 128) {
        return MakeErrorResponse(c, "The principle of parsimony is not upheld in your contact details. Please be more concise.")
    }

    var uidForDb string
    if args.Anonymous {
        uidForDb = ""
    } else {
        uidForDb = uid
    }

    var contactDetailsForDb string
    if args.Anonymous {
        contactDetailsForDb = ""
    } else {
        contactDetailsForDb = args.Contact
    }

    errored := AddFeedbackToDb(args.Message, args.EmotionalState, uidForDb, contactDetailsForDb)
    if errored {
        return MakeErrorResponse(c, "An error ocurred while processing your valuable feedback.")
    }
    
    return c.JSON(fiber.Map{"success": true})
}

func HandleGetUpdatesAPIRequest(c *fiber.Ctx) error {
    uid := c.Locals("user").(gofiberfirebaseauth.User).UserID
    
    updates := GetUpdatesForUser(uid)
    if updates == nil {
        return MakeErrorResponse(c, "Could not get updates :(")
    }
    var updates_JSON []interface{}
    for _, update := range updates {
        updates_JSON = append(updates_JSON, fiber.Map{
            "name": update.name,
            "title": update.title,
            "description": update.description,
            "learn_more_link": update.learn_more_link,
        })
    }
    
    return c.JSON(fiber.Map{
        "updates": updates_JSON,
    });
}

func HandleReadUpdatesAPIRequest(c *fiber.Ctx) error {
    uid := c.Locals("user").(gofiberfirebaseauth.User).UserID
    
    err := MarkUpdatesAsReadForUser(uid)
    if err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "An error ocurred while interacting with the database :(")
    }
    
    return c.JSON(fiber.Map{
        "success": true,
    });
}

func HandleGetUnresolveFeedbackAPIRequest(c *fiber.Ctx) error {
    user := c.Locals("user").(gofiberfirebaseauth.User)
    email := user.Email

    if email != ADMIN_EMAIL {
        return MakeErrorResponse(c, "only the admin can access this endpoint!")
    }
    
    feedback := GetUnresolvedFeedback()
    if feedback == nil {
        return MakeErrorResponse(c, "Could not get unresolved feedback :(")
    }

    var feedback_JSON []interface{}
    for _, item := range feedback {
        feedback_JSON = append(feedback_JSON, fiber.Map{
            "id": item.id,
            "feedback": item.feedback,
            "emotional_state": item.emotional_state,
            "uid": item.uid.String,
            "contact": item.contact.String,
            "resolved": item.resolved,
        })
    }
    
    return c.JSON(fiber.Map{
        "unresolved_feedback": feedback_JSON,
    });
}

type ResolveFeedbackArgs struct {
    Id int `json:"id"`
}

func HandleResolveFeedbackAPIRequest(c *fiber.Ctx) error {
    user := c.Locals("user").(gofiberfirebaseauth.User)
    email := user.Email

    if email != ADMIN_EMAIL {
        return MakeErrorResponse(c, "only the admin can access this endpoint!")
    }
    
    args := new(ResolveFeedbackArgs)
    if err := c.BodyParser(args); err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "error ocurred while decoding input data")
    }

    if args.Id < 1 {
        return MakeErrorResponse(c, "come on man - you know better than anyone that the id should be greater than or equal to 1 :(")
    }

    err := MarkFeedbackAsResolved(args.Id)
    if err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "error while marking feedback as resolved :(")
    }

    return c.JSON(fiber.Map{
        "success": true,
    });
}

type VerifyEmailArgs struct {
    Email string `json:"email"`
}

func HandleVerifyEmailAPIRequest(c *fiber.Ctx, app *firebase.App) error {
    user := c.Locals("user").(gofiberfirebaseauth.User)
    email := user.Email

    if email != ADMIN_EMAIL {
        return MakeErrorResponse(c, "only the admin can access this endpoint!")
    }
    
    args := new(VerifyEmailArgs)
    if err := c.BodyParser(args); err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "error ocurred while decoding input data")
    }

    if len(args.Email) < 2 {
        return MakeErrorResponse(c, "got email?") // got milk?
    }

    authClient, err := app.Auth(context.Background())
    if err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "error while intitializing admin Auth thingy")
    }

    target_user, err := authClient.GetUserByEmail(context.Background(), args.Email)
    if err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "error while fetching user by email")
    }
    target_user_uid := target_user.UID

    update := (&auth.UserToUpdate{}).EmailVerified(true)
    _, err = authClient.UpdateUser(context.Background(), target_user_uid, update)
    if err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "error while updating")
    }

    return c.JSON(fiber.Map{
        "success": true,
    });
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
        panic("error initializing Firebase app: " + err.Error())
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
    api.Post("/feedback", HandleCreateFeedbackAPIRequest)
    api.Get("/updates", HandleGetUpdatesAPIRequest)
    api.Post("/updates", HandleReadUpdatesAPIRequest)
    api.Get("/unresolved-feedback", HandleGetUnresolveFeedbackAPIRequest)
    api.Post("/resolve-feedback", HandleResolveFeedbackAPIRequest)
    api.Post("/verify-email", func (c *fiber.Ctx) error {
        return HandleVerifyEmailAPIRequest(c, fbapp);
    })
}