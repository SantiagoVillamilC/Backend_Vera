package receipts

import ("context"; "fmt"; "net/http"; "os")
type OCRClient struct { baseURL string; httpClient *http.Client }
func NewOCRClient() (*OCRClient,error) { url:=os.Getenv("OCR_WORKER_URL");if url==""{return nil,fmt.Errorf("OCR_WORKER_URL is required")};return &OCRClient{baseURL:url,httpClient:http.DefaultClient},nil }
func(c *OCRClient) Extract(ctx context.Context,image []byte)(Receipt,error){request,err:=http.NewRequestWithContext(ctx,http.MethodPost,c.baseURL+"/extract",nil);if err!=nil{return Receipt{},err};response,err:=c.httpClient.Do(request);if err!=nil{return Receipt{},err};defer response.Body.Close();if response.StatusCode>=300{return Receipt{},fmt.Errorf("OCR worker returned %s",response.Status)};return Receipt{},fmt.Errorf("TODO: decode OCR worker response")}
