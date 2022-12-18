package main

import (
    "time"
    "github.com/stripe/stripe-go/v74"
    "github.com/stripe/stripe-go/v74/usagerecord"
)

func BillEveryone() {
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

func StripeBillRoutine() {
    time.Sleep(10 * time.Second) // allow everything to boot up
    for true {
        BillEveryone()
        time.Sleep(5 * time.Minute)
    }
}