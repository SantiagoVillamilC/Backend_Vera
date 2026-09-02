package receipts

import("context";"fmt")
type Extractor interface { Extract(context.Context,[]byte)(Receipt,error) }
type Service struct{client Extractor}
func NewService(client Extractor)*Service{return &Service{client:client}}
func(s *Service) Process(ctx context.Context,image []byte)(Receipt,error){if s.client==nil{return Receipt{},fmt.Errorf("OCR client is not configured")};return s.client.Extract(ctx,image)}
