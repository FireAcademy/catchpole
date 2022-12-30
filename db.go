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
)

var DB *sql.DB

func GetWeekId() string {
    // https://stackoverflow.com/questions/47193649/week-number-based-on-timestamp-with-go
    tn := time.Now().UTC()
    year, week := tn.ISOWeek()

    return fmt.Sprintf("%d-%d", year, week)
}

func GetAPIKeyAndSubscribed(api_key string) (*APIKey, bool) {
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

func DecreaseCreditsToBill(api_key string, credits int64) error {
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
func GetUserBillingInfo() (string, int64) {
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

    err = DecreaseCreditsToBill(apiKey, credits)
    if err != nil {
        log.Print(err)
        return "", 0
    }

    return stripeItemId, credits
}

func GetWeeklyUsage(api_key string) *WeeklyUsage {
    week_id := GetWeekId()
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

func GetAPIKeyAndWeeklyUsage(api_key string) (*APIKey, *WeeklyUsage, bool) {
    week_id := GetWeekId()
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

func CreateWeeklyUsage(api_key string) *WeeklyUsage {
    week_id := GetWeekId()
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

    return GetWeeklyUsage(api_key)
}

func DecreaseAPIKeyFreeUsage(api_key string, credits int64) error {
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

func IncreaseWeeklyUsage(api_key string, credits int64) error {
    week_id := GetWeekId()
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

func BillCredits(api_key string, uid string, credits int64) error {
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

func UpdateCustomerBillingDetails(
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

func UpdateCustomerActiveSubscription(
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

func RevokeAPIKeys(
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

func SetupDB() {
    db_conn_string := os.Getenv("DB_CONN_STRING")
    if db_conn_string == "" {
        panic("DB_CONN_STRING not specified, exiting :(\n")
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
