package copilot

import ("context"; "fmt")
type Completer interface { Complete(context.Context,[]ChatMessage)(string,error) }
type Service struct { client Completer }
func NewService(client Completer)*Service{return &Service{client:client}}
func(s *Service) Respond(ctx context.Context,req Request)(Response,error){if s.client==nil{return Response{},fmt.Errorf("LLM client is not configured")};message,err:=s.client.Complete(ctx,req.Messages);if err!=nil{return Response{},err};return Response{Intent:IntentQuery,Message:message},nil}
