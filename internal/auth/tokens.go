package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	// duracionAccess es la vida del access, que el contrato baja a 5 minutos.
	duracionAccess = 5 * time.Minute
	// algoritmoAccess es el único algoritmo aceptado al verificar el access.
	algoritmoAccess = "HS256"
)

// ErrAccessInvalido es el access vencido, alterado o con firma inválida; responde 401.
var ErrAccessInvalido = errors.New("auth: el access no es válido")

// ClaimsAccess es el contenido del access que viaja firmado.
type ClaimsAccess struct {
	ParteID   uuid.UUID
	SesionID  uuid.UUID
	Contexto  string
	Rol       string
	JTI       uuid.UUID
	EmitidoEn time.Time
	ExpiraEn  time.Time
}

// TokensDeAcceso firma y verifica los access JWT con HS256.
type TokensDeAcceso struct {
	secreto   []byte
	emisor    string
	audiencia string
	ahora     func() time.Time
}

// NuevoTokensDeAcceso exige el secreto de firma, el emisor y la audiencia del access.
func NuevoTokensDeAcceso(secreto []byte, emisor, audiencia string) (*TokensDeAcceso, error) {
	if len(secreto) == 0 {
		return nil, errors.New("auth: el access necesita un secreto de firma")
	}
	if emisor == "" || audiencia == "" {
		return nil, errors.New("auth: el access necesita emisor y audiencia")
	}
	return &TokensDeAcceso{secreto: secreto, emisor: emisor, audiencia: audiencia, ahora: time.Now}, nil
}

// claimsFirmados es la forma serializada de ClaimsAccess como JWT.
type claimsFirmados struct {
	Sid string `json:"sid"`
	Ctx string `json:"ctx"`
	Rol string `json:"rol"`
	jwt.RegisteredClaims
}

// Firmar devuelve el access firmado con los claims de la sesión.
func (t *TokensDeAcceso) Firmar(claims ClaimsAccess) (string, error) {
	if claims.EmitidoEn.IsZero() {
		claims.EmitidoEn = t.ahora().UTC()
	}
	if claims.ExpiraEn.IsZero() {
		claims.ExpiraEn = claims.EmitidoEn.Add(duracionAccess)
	}
	if claims.JTI == uuid.Nil {
		claims.JTI = uuid.New()
	}
	firmados := claimsFirmados{
		Sid: claims.SesionID.String(),
		Ctx: claims.Contexto,
		Rol: claims.Rol,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    t.emisor,
			Audience:  jwt.ClaimStrings{t.audiencia},
			Subject:   claims.ParteID.String(),
			ID:        claims.JTI.String(),
			IssuedAt:  jwt.NewNumericDate(claims.EmitidoEn),
			ExpiresAt: jwt.NewNumericDate(claims.ExpiraEn),
		},
	}
	firmado, err := jwt.NewWithClaims(jwt.SigningMethodHS256, firmados).SignedString(t.secreto)
	if err != nil {
		return "", fmt.Errorf("auth: firmar el access: %w", err)
	}
	return firmado, nil
}

// Verificar valida firma, algoritmo, emisor, audiencia y vencimiento y devuelve los claims.
func (t *TokensDeAcceso) Verificar(token string) (ClaimsAccess, error) {
	claims := &claimsFirmados{}
	if _, err := jwt.ParseWithClaims(token, claims,
		func(*jwt.Token) (any, error) { return t.secreto, nil },
		jwt.WithValidMethods([]string{algoritmoAccess}),
		jwt.WithIssuer(t.emisor),
		jwt.WithAudience(t.audiencia),
		jwt.WithExpirationRequired(),
	); err != nil {
		return ClaimsAccess{}, fmt.Errorf("%w: %v", ErrAccessInvalido, err)
	}
	return claims.accessDeDominio()
}

// accessDeDominio mapea los claims verificados al tipo de dominio y exige los identificadores.
func (c claimsFirmados) accessDeDominio() (ClaimsAccess, error) {
	parteID, err := uuid.Parse(c.Subject)
	if err != nil {
		return ClaimsAccess{}, ErrAccessInvalido
	}
	sesionID, err := uuid.Parse(c.Sid)
	if err != nil {
		return ClaimsAccess{}, ErrAccessInvalido
	}
	jti, err := uuid.Parse(c.ID)
	if err != nil {
		return ClaimsAccess{}, ErrAccessInvalido
	}
	if c.ExpiresAt == nil || c.IssuedAt == nil {
		return ClaimsAccess{}, ErrAccessInvalido
	}
	return ClaimsAccess{
		ParteID:   parteID,
		SesionID:  sesionID,
		Contexto:  c.Ctx,
		Rol:       c.Rol,
		JTI:       jti,
		EmitidoEn: c.IssuedAt.Time,
		ExpiraEn:  c.ExpiresAt.Time,
	}, nil
}
