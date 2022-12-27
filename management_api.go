package main

import (
    "os"
    "github.com/gofiber/fiber/v2"
)

// Field names should start with an uppercase letter
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

    // check args.Name
    if args.Credits < 1 {
        return MakeErrorResponse(c, "number of billed credits should be a positive number")
    }
    
    errored := TaxAPIKey(args.APIKey, aegs.Credits)
    if errored {
        return MakeErrorResponse(c, "error while billing credits")
    }

    return c.JSON(fiber.Map{
        "success": true,
    });
}

func SetupManagementAPIRoutes(app *fiber.App) {
    api := app.Group("/management")
    management_token := os.Getenv("CATCHPOLE_MANAGEMENT_TOKEN")
    if management_token == "" {
        panic("CATCHPOLE_MANAGEMENT_TOKEN not set.")
    }

    api.Use(
        // todo: verify given token in header with management_token
    )
    
    api.Post("/bill-credits", HandleBillCreditsAPIRequest)
}