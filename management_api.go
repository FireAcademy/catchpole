package main

import (
    "os"
    "github.com/gofiber/fiber/v2"
)

type BillCreditsArgs struct {
    APIKey string `json:"api_key"`
    Credits int64 `json:"credits"`
}

func HandleBillCreditsAPIRequest(c *fiber.Ctx) error {
    args := new(BillCreditsArgs)

    if err := c.BodyParser(args); err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "error ocurred while decoding input data")
    }

    // check args.APIKey
    if len(args.APIKey) != 36 {
        return MakeErrorResponse(c, "API keys are 36 chars long")
    }

    // check args.Credits
    if args.Credits < 1 {
        return MakeErrorResponse(c, "number of billed credits should be a positive number")
    }
    
    errored := TaxAPIKey(args.APIKey, args.Credits)
    if errored {
        return MakeErrorResponse(c, "error while billing credits")
    }

    return c.JSON(fiber.Map{
        "success": true,
    });
}

type IsAPIKeyActiveArgs struct {
    APIKey string `json:"api_key"`
    Credits int64 `json:"credits"`
}

func HandleIsAPIKeyActiveArgsAPIRequest(c *fiber.Ctx) error {
    args := new(BillCreditsArgs)

    if err := c.BodyParser(args); err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "error ocurred while decoding input data")
    }

    // check args.APIKey
    if len(args.APIKey) != 36 {
        return MakeErrorResponse(c, "API keys are 36 chars long")
    }

    // check args.Credits
    if args.Credits < 1 {
        return MakeErrorResponse(c, "number of billed credits should be a positive number")
    }

    _, _, active := CheckAPIKeyAndReturnAPIKeyAndSubscribed(args.APIKey, args.Credits)

    return c.JSON(fiber.Map{
        "success": true,
        "active": active,
    });
}

func SetupManagementAPIRoutes(app *fiber.App) {
    api := app.Group("/management")
    management_token := os.Getenv("CATCHPOLE_MANAGEMENT_TOKEN")
    if management_token == "" {
        panic("CATCHPOLE_MANAGEMENT_TOKEN not set.")
    }

    api.Use(func(c *fiber.Ctx) error {
        mgmt_tok := c.Get("X-Management-Token")
        if mgmt_tok != management_token {
            return MakeErrorResponse(c, "X-Management-Token has incorrect value")
        }

        return c.Next()
    })
    
    api.Post("/bill-credits", HandleBillCreditsAPIRequest)
    api.Post("/is-api-key-active", HandleIsAPIKeyActiveArgsAPIRequest)
}