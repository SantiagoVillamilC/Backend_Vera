package receipts

type Receipt struct {
	ID       string        `json:"id"`
	UserID   string        `json:"user_id"`
	Merchant string        `json:"merchant"`
	Total    float64       `json:"total"`
	Items    []ReceiptItem `json:"items"`
}

type ReceiptItem struct {
	Name     string  `json:"name"`
	Quantity float64 `json:"quantity"`
	Price    float64 `json:"price"`
}
