package main

import (
    "os"
    "fmt"
    "log"
    "time"
    "bytes"
    "errors"
    "strconv"
    "context"
    "net/http"
    "io/ioutil"
    "database/sql"
    _ "github.com/lib/pq"
    "github.com/google/uuid"
    "github.com/gofiber/fiber/v2"
    "google.golang.org/api/option"
    firebase "firebase.google.com/go"
    "github.com/stripe/stripe-go/v74"
    "github.com/stripe/stripe-go/v74/webhook"
    "github.com/sacsand/gofiber-firebaseauth"
    "github.com/stripe/stripe-go/v74/customer"
    "github.com/stripe/stripe-go/v74/subscription"
    "github.com/gofiber/fiber/v2/middleware/monitor"
    "github.com/gofiber/fiber/v2/middleware/basicauth"
    "github.com/stripe/stripe-go/v74/checkout/session"
    portalsession "github.com/stripe/stripe-go/v74/billingportal/session"
)

type APIKey struct {
    api_key string
    disabled bool
    free_credits_remaining int64
    weekly_credit_limit int64
    name string
    origin string
    uid string
}

type WeeklyUsage struct {
    id int64
    api_key string
    credits int64
    week string
}

type User struct {
    uid string
    received_free_credits bool
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

type GiftCodeAttempts struct {
    uid string
    fails int64
}

var db *sql.DB;

func getWeekId() string {
    // https://stackoverflow.com/questions/47193649/week-number-based-on-timestamp-with-go
    tn := time.Now().UTC()
    year, week := tn.ISOWeek()

    return fmt.Sprintf("%d-%d", year, week)
}

func getAPIKey(api_key string) *APIKey {
    row := db.QueryRow("SELECT * FROM api_keys WHERE api_key = $1", api_key)

    apiKey := new(APIKey)
    err := row.Scan(
        &apiKey.api_key,
        &apiKey.disabled,
        &apiKey.free_credits_remaining,
        &apiKey.weekly_credit_limit,
        &apiKey.name,
        &apiKey.origin,
        &apiKey.uid,
    )

    if err == sql.ErrNoRows {
        return nil
    }
    if err != nil {
        log.Print(err)
        return nil
    }
    return apiKey
}

func getAPIKeysForUser(uid string) []*APIKey {
    rows, err := db.Query("SELECT * FROM api_keys WHERE uid = $1", uid)
    if err != nil {
        log.Print(err)
        return nil
    }
    defer rows.Close()

    apiKeys := make([]*APIKey, 0)
    for rows.Next() {
        apiKey := new(APIKey)
        err := rows.Scan(
            &apiKey.api_key,
            &apiKey.disabled,
            &apiKey.free_credits_remaining,
            &apiKey.weekly_credit_limit,
            &apiKey.name,
            &apiKey.origin,
            &apiKey.uid,
        )
        if err != nil {
            log.Print(err)
            return nil
        }
        apiKeys = append(apiKeys, apiKey)
    }
    if err = rows.Err(); err != nil {
        log.Print(err)
        return nil
    }

    return apiKeys
}

func getWeeklyUsagesForUser(uid string) []*WeeklyUsage {
    week_id := getWeekId()
    rows, err := db.Query("SELECT * FROM weekly_usage WHERE week = $1 AND api_key IN (SELECT api_key FROM api_keys WHERE uid = $2)", week_id, uid)
    if err != nil {
        log.Print(err)
        return nil
    }
    defer rows.Close()

    weeklyUsages := make([]*WeeklyUsage, 0)
    for rows.Next() {
        weeklyUsage := new(WeeklyUsage)
        err := rows.Scan(
            &weeklyUsage.id,
            &weeklyUsage.api_key,
            &weeklyUsage.credits,
            &weeklyUsage.week,
        )
        if err != nil {
            log.Print(err)
            return nil
        }
        weeklyUsages = append(weeklyUsages, weeklyUsage)
    }
    if err = rows.Err(); err != nil {
        log.Print(err)
        return nil
    }

    return weeklyUsages
}

func getUser(uid string) *User {
    row := db.QueryRow("SELECT * FROM users WHERE uid = $1", uid)

    user := new(User)
    err := row.Scan(
        &user.uid,
        &user.received_free_credits,
        &user.has_active_stripe_subscription,
        &user.stripe_user_id,
        &user.stripe_item_id,
        &user.stripe_subscription_id,
        &user.stripe_product_id,
        &user.stripe_price_id,
    )
    
    if err == sql.ErrNoRows {
        _, err = db.Exec(
            "INSERT INTO users(uid, received_free_credits, has_active_stripe_subscription) VALUES($1, false, false)",
            uid,
        )
        if err != nil {
            return getUser(uid)
        }
    }

    if err != nil {
        log.Print(err)
        return nil
    }
    return user
}

func getGiftCodeAttempts(uid string) *GiftCodeAttempts {
    row := db.QueryRow("SELECT * FROM gift_code_attempts WHERE uid = $1", uid)

    gca := new(GiftCodeAttempts)
    err := row.Scan(
        &gca.uid,
        &gca.fails,
    )
    
    if err == sql.ErrNoRows {
        _, err = db.Exec(
            "INSERT INTO gift_code_attempts(uid, fails) VALUES($1, 0)",
            uid,
        )
        if err != nil {
            return getGiftCodeAttempts(uid)
        }
    }

    if err != nil {
        log.Print(err)
        return nil
    }
    return gca
}

func getGiftCode(code string) *GiftCode {
    row := db.QueryRow("SELECT * FROM gift_codes WHERE code = $1", code)

    giftCode := new(GiftCode)
    err := row.Scan(
        &giftCode.code,
        &giftCode.credits,
        &giftCode.used,
        &giftCode.uid,
    )
    
    if err == sql.ErrNoRows {
        return nil
    }
    if err != nil {
        log.Print(err)
        return nil
    }

    return giftCode
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

func getAPIKeyAndWeeklyUsage(api_key string) (*APIKey, *WeeklyUsage) {
    week_id := getWeekId()
    row := db.QueryRow("SELECT " + 
        "weekly_usage.id, weekly_usage.api_key, weekly_usage.credits, weekly_usage.week, " +
        "api_keys.api_key, api_keys.disabled, api_keys.free_credits_remaining, api_keys.weekly_credit_limit, api_keys.name, api_keys.origin, api_keys.uid " +
        "FROM weekly_usage LEFT JOIN api_keys " + 
        "ON api_keys.api_key = weekly_usage.api_key " + 
        "AND weekly_usage.api_key = $1 " + 
        "AND weekly_usage.week = $2", api_key, week_id)

    weeklyUsage := new(WeeklyUsage)
    apiKey := new(APIKey)
    err := row.Scan(
        &weeklyUsage.id,
        &weeklyUsage.api_key,
        &weeklyUsage.credits,
        &weeklyUsage.week,
        &apiKey.api_key,
        &apiKey.disabled,
        &apiKey.free_credits_remaining,
        &apiKey.weekly_credit_limit,
        &apiKey.name,
        &apiKey.origin,
        &apiKey.uid,
    )
    if err == sql.ErrNoRows {
        return nil, nil
    } else if err != nil {
        log.Print(err)
        return nil, nil
    }

    return apiKey, weeklyUsage
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

func createAPIKey(uid string, free_credits int64, weekly_credit_limit int64, name string, origin string) error {
    id := uuid.New()
    api_key := id.String()
    result, err := db.Exec(
        "INSERT INTO " + 
        "api_keys(api_key, disabled, free_credits_remaining, weekly_credit_limit, name, origin, uid) " + 
        "VALUES($1, false, $2, $3, $4, $5, $6)",
        api_key, free_credits, weekly_credit_limit, name, origin, uid,
    )
    if err != nil {
        log.Print(err)
        return err
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        log.Print(err)
        return err
    }

    if rowsAffected != 1 {
        err = errors.New(api_key + " -> ????? (0 or more than 1 row affected in createAPIKey)")
        return err
    }

    return nil
}

func generateGiftCode(credits int64) (string, error) {
    id := uuid.New()
    gift_code := id.String()
    result, err := db.Exec(
        "INSERT INTO " + 
        "gift_codes(code, credits, used) " + 
        "VALUES($1, $2, false)",
        gift_code, credits,
    )
    if err != nil {
        log.Print(err)
        return "", err
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        log.Print(err)
        return "", err
    }

    if rowsAffected != 1 {
        err = errors.New(gift_code + " -> ????? (0 or more than 1 row affected in generateGiftCode)")
        return "", err
    }

    return gift_code, nil
}

func decreaseAPIKeyFreeUsage(api_key string, credits int64) error {
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

func increaseAPIKeyFreeUsage(api_key string, credits int64) error {
    result, err := db.Exec(
        "UPDATE api_keys SET free_credits_remaining = free_credits_remaining + $1 WHERE api_key = $2",
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
        err = errors.New(api_key + " -> ????? (0 or more than 1 row affected in increaseAPIKeyFreeUsage)")
        return err
    }

    return nil
}

func markGiftCodeAsUsed(code string, uid string) error {
    result, err := db.Exec(
        "UPDATE gift_codes SET used = true, uid = $1 WHERE code = $2 AND used = false",
        uid, code,
    )
    if err != nil {
        return err
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        return err
    }

    if rowsAffected != 1 {
        err = errors.New(code + ":" + uid + " -> ????? (0 or more than 1 row affected in markGiftCodeAsUsed)")
        return err
    }

    return nil
}

func increaseGiftCodeAttempts(uid string) error {
    result, err := db.Exec(
        "UPDATE gift_code_attempts SET fails = fails + 1 WHERE uid = $1",
        uid,
    )
    if err != nil {
        return err
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        return err
    }

    if rowsAffected != 1 {
        err = errors.New(uid + " -> ????? (0 or more than 1 row affected in increaseGiftCodeAttempts)")
        return err
    }

    return nil
}

func updateAPIKey(api_key string, disabled bool, weekly_credit_limit int64, name string, origin string) error {
    result, err := db.Exec(
        "UPDATE api_keys SET" +
        " disabled = $1," +
        " weekly_credit_limit = $2," +
        " name = $3," +
        " origin = $4 " +
        "WHERE api_key = $5",
        disabled, weekly_credit_limit, name, origin, api_key,
    )
    if err != nil {
        return err
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        return err
    }

    if rowsAffected != 1 {
        err = errors.New(api_key + " -> ????? (0 or more than 1 row affected in updateAPIKey)")
        return err
    }

    return nil
}

func updateUserReceivedFreeCredits(uid string, received_free_credits bool) error {
    result, err := db.Exec(
        "UPDATE users SET received_free_credits = $1 WHERE uid = $2",
        received_free_credits, uid,
    )
    if err != nil {
        return err
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        return err
    }

    if rowsAffected != 1 {
        err = errors.New(uid + " -> ????? (0 or more than 1 row affected in updateUserReceivedFreeCredits)")
        return err
    }

    return nil
}

func increaseWeeklyUsage(api_key string, credits int64) error {
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
        err = errors.New(api_key + " -> ????? (0 or more than 1 row affected in increaseWeeklyUsage, #1)")
        return err
    }

    return nil
}

func billCredits(api_key string, uid string, credits int64) error {
    result, err := db.Exec(
        "UPDATE credits_to_bill SET credits = credits + $1 WHERE api_key = $2",
        credits, api_key,
    )
    if err != nil {
        return err
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        return err
    }

    if rowsAffected < 1 {
        _, err = db.Exec(
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

func updateUserStripeId(
    uid string,
    stripe_user_id string,
) error {
    result, err := db.Exec(
        "UPDATE users SET stripe_user_id = $1 WHERE uid = $2",
        stripe_user_id, uid,
    )
    if err != nil {
        return err
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        return err
    }

    if rowsAffected > 1 {
        err = errors.New(stripe_user_id + " -> ????? (more than 1 rows affected in updateUserStripeId)")
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

func taxTrafficAndReturnOrigin(api_key string, endpoint string, credits_per_request int64) (string /*origin*/, bool /*errored*/) {
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

func leafletHandler(c *fiber.Ctx, api_key string, endpoint string, leaflet_base_url string) error {
    const CREDITS_PER_REQUEST = 420;
    origin, errored := taxTrafficAndReturnOrigin(api_key, endpoint, CREDITS_PER_REQUEST)
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
        case "checkout.session.completed":
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
    var err error
    db, err = sql.Open("postgres", db_conn_string)
    if err != nil {
        panic(err)
    }
    defer db.Close()

    err = db.Ping()
    if err != nil {
        panic(err)
    }

    max_idle_conns := os.Getenv("DB_MAX_IDLE_CONNS")
    if max_idle_conns == "" {
        panic("DB_MAX_IDLE_CONNS not set")
    }
    max_open_conns := os.Getenv("DB_MAX_OPEN_CONNS")
    if max_open_conns == "" {
        panic("DB_MAX_OPEN_CONNS not set")
    }

    max_idle_conns_i, err := strconv.Atoi(max_idle_conns)
    if err != nil {
        panic(err)
    }
    max_open_conns_i, err := strconv.Atoi(max_open_conns)
    if err != nil {
        panic(err)
    }
    // Maximum Idle Connections
    db.SetMaxIdleConns(max_idle_conns_i)
    // Maximum Open Connections
    db.SetMaxOpenConns(max_open_conns_i)
    // Idle Connection Timeout - no need!
    // db.SetConnMaxIdleTime(1 * time.Second)
    // Connection Lifetime
    db.SetConnMaxLifetime(30 * time.Second)

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

    // Start server
    log.Fatalln(app.Listen(fmt.Sprintf(":%v", port)))
}
