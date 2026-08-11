// Card payments: the PAN is masked to PCI's first-6/last-4 and the CVV never
// ships, even though the whole struct is logged as-is.
package main

import (
	"context"

	"github.com/Wigata-Intech/w-tools/logger"
)

type Card struct {
	Number string `json:"card_number"`
	CVV    string `json:"cvv"`
	Holder string `json:"holder"`
}

func main() {
	log := logger.New(logger.Config{
		Env:      "production",
		Version:  "1.0.0",
		App:      "payments",
		Protocol: logger.ProtocolHTTP,
		Redact: logger.RedactConfig{
			Redacted: []string{"cvv"},
			Masked:   map[string]logger.Mask{"card_number": {ShowFirst: 6, ShowLast: 4}},
		},
	})

	ctx := context.Background()
	card := Card{Number: "4111111111111111", CVV: "123", Holder: "DHIRA WIGATA"}
	log.Info(ctx, "charge created", "order_id", "ord_1042", "amount_jpy", 4980, "card", card)
	// {"time":"...","level":"INFO","msg":"charge created","env":"production","version":"1.0.0",
	//  "app":"payments","protocol":"http","order_id":"ord_1042","amount_jpy":4980,
	//  "card":{"card_number":"411111******1111","cvv":"[REDACTED]","holder":"DHIRA WIGATA"}}
}
