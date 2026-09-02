package receipts

type Receipt struct { ID, UserID, Merchant string; Total float64; Items []ReceiptItem }
type ReceiptItem struct { Name string; Quantity, Price float64 }
