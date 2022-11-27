package main

import (
	"os"
    "fmt"
    "log"
    "time"
    "errors"
    "strconv"
    "database/sql"
    _ "github.com/lib/pq"
    "github.com/google/uuid"
)

var DB *sql.DB

func getWeekId() string {
    // https://stackoverflow.com/questions/47193649/week-number-based-on-timestamp-with-go
    tn := time.Now().UTC()
    year, week := tn.ISOWeek()

    return fmt.Sprintf("%d-%d", year, week)
}

func getAPIKey(api_key string) *APIKey {
    row := DB.QueryRow("SELECT * FROM api_keys WHERE api_key = $1", api_key)

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

func getAPIKeyAndSubscribed(api_key string) (*APIKey, bool) {
    row := DB.QueryRow("SELECT * FROM api_keys WHERE api_key = $1", api_key)

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
        return nil, false
    }
    if err != nil {
        log.Print(err)
        return nil, false
    }

    row = DB.QueryRow("SELECT has_active_stripe_subscription FROM users WHERE uid = $1", apiKey.uid)

    var subscribed bool
    err = row.Scan(&subscribed)

    if err == sql.ErrNoRows {
        return nil, false
    }
    if err != nil {
        log.Print(err)
        return nil, false
    }
    return apiKey, subscribed
}

func getAPIKeysForUser(uid string) []*APIKey {
    rows, err := DB.Query("SELECT * FROM api_keys WHERE uid = $1", uid)
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

func decreaseCreditsToBill(api_key string, credits int64) error {
    result, err := DB.Exec(
        "UPDATE credits_to_bill SET credits = credits - $1 WHERE api_key = $2",
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
        err = errors.New(api_key + " -> ????? (0 or more than 1 row affected in decreaseCreditsToBill)")
        return err
    }

    return nil
}

// returns (stripe_item_id, credits)
func getUserBillingInfo() (string, int64) {
    row := DB.QueryRow("SELECT " + 
        "credits_to_bill.credits, credits_to_bill.api_key, users.stripe_item_id " + 
        "FROM credits_to_bill " + 
        "JOIN users" +
        " ON users.uid = credits_to_bill.uid" + 
        " AND credits_to_bill.credits > 0" + 
        " AND users.has_active_stripe_subscription = true " +
        "LIMIT 1",
    )
    var credits int64
    var apiKey string
    var stripeItemId string

    err := row.Scan(
        &credits,
        &apiKey,
        &stripeItemId,
    )

    if err == sql.ErrNoRows {
        return "", 0
    }
    if err != nil {
        log.Print(err)
        return "", 0
    }

    err = decreaseCreditsToBill(apiKey, credits)
    if err != nil {
        log.Print(err)
        return "", 0
    }

    return stripeItemId, credits
}

func getWeeklyUsagesForUser(uid string) []*WeeklyUsage {
    week_id := getWeekId()
    rows, err := DB.Query("SELECT * FROM weekly_usage WHERE week = $1 AND api_key IN (SELECT api_key FROM api_keys WHERE uid = $2)", week_id, uid)
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
    row := DB.QueryRow("SELECT * FROM users WHERE uid = $1", uid)

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
        _, err = DB.Exec(
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
    row := DB.QueryRow("SELECT * FROM gift_code_attempts WHERE uid = $1", uid)

    gca := new(GiftCodeAttempts)
    err := row.Scan(
        &gca.uid,
        &gca.fails,
    )
    
    if err == sql.ErrNoRows {
        _, err = DB.Exec(
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
    row := DB.QueryRow("SELECT * FROM gift_codes WHERE code = $1", code)

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
    row := DB.QueryRow("SELECT * FROM weekly_usage WHERE api_key = $1 AND week = $2", api_key, week_id)

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

func getAPIKeyAndWeeklyUsage(api_key string) (*APIKey, *WeeklyUsage, bool) {
    week_id := getWeekId()
    row := DB.QueryRow("SELECT " + 
        "weekly_usage.id, weekly_usage.api_key, weekly_usage.credits, weekly_usage.week, " +
        "api_keys.api_key, api_keys.disabled, api_keys.free_credits_remaining, api_keys.weekly_credit_limit, api_keys.name, api_keys.origin, api_keys.uid, " +
        "users.has_active_stripe_subscription " + 
        "FROM weekly_usage LEFT JOIN api_keys" + 
        " ON api_keys.api_key = weekly_usage.api_key" + 
        " AND weekly_usage.api_key = $1" + 
        " AND weekly_usage.week = $2 " +
        "LEFT JOIN users WHERE api_keys.uid = users.uid", api_key, week_id)

    weeklyUsage := new(WeeklyUsage)
    apiKey := new(APIKey)
    var subscribed bool
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
        &subscribed,
    )
    if err == sql.ErrNoRows {
        return nil, nil, false
    } else if err != nil {
        return nil, nil, false
    }

    return apiKey, weeklyUsage, subscribed
}

func createWeeklyUsage(api_key string) *WeeklyUsage {
    week_id := getWeekId()
    result, err := DB.Exec(
        // prevent race conditions
        "INSERT INTO weekly_usage(api_key, credits, week) SELECT $1, $2, $3 WHERE NOT EXISTS (SELECT 1 FROM weekly_usage WHERE api_key = $1 AND week = $3)",
        api_key, 0, week_id,
    )
    if err != nil {
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
    result, err := DB.Exec(
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
    result, err := DB.Exec(
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
    result, err := DB.Exec(
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
    result, err := DB.Exec(
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
    result, err := DB.Exec(
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
    result, err := DB.Exec(
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
    result, err := DB.Exec(
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
    result, err := DB.Exec(
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
    result, err := DB.Exec(
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
    result, err := DB.Exec(
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
        _, err = DB.Exec(
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
    result, err := DB.Exec(
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
    result, err := DB.Exec(
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
    result, err := DB.Exec(
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
    _, err := DB.Exec(
        "UPDATE api_keys SET disabled = true WHERE uid = $1",
        uid,
    )
    if err != nil {
        return err
    }

    return nil
}

func setupDB() {
    db_conn_string := os.Getenv("DB_CONN_STRING")
    if db_conn_string == "" {
        fmt.Printf("DB_CONN_STRING not specified, exiting :(\n")
        return
    }
    var err error
    DB, err = sql.Open("postgres", db_conn_string)
    if err != nil {
        panic(err)
    }

    err = DB.Ping()
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
    DB.SetMaxIdleConns(max_idle_conns_i)
    // Maximum Open Connections
    DB.SetMaxOpenConns(max_open_conns_i)
    // Idle Connection Timeout - no need!
    // DB.SetConnMaxIdleTime(1 * time.Second)
    // Connection Lifetime
    DB.SetConnMaxLifetime(30 * time.Second)
}
