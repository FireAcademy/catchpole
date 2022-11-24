package main

import (
    "database/sql"
    _ "github.com/lib/pq"
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