package transactions

import ("context"; "fmt")

type ConfirmationVerifier interface { Verify(context.Context, string, string) error }
type ConfirmationService struct { verifier ConfirmationVerifier }
func NewConfirmationService(verifier ConfirmationVerifier) *ConfirmationService { return &ConfirmationService{verifier:verifier} }
func (s *ConfirmationService) Require(ctx context.Context, userID, token string) error {
	// Ninguna transacción puede ejecutarse sin confirmación previa (huella/PIN) del lado de la app.
	if token=="" { return fmt.Errorf("confirmation (fingerprint/PIN) is required") }
	if s.verifier!=nil { return s.verifier.Verify(ctx,userID,token) }
	return nil
}
