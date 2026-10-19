package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// clienteNucleo confirma el depósito en POST /depositos/confirmaciones del núcleo.
type clienteNucleo struct {
	url     string
	token   string
	cliente *http.Client
}

// Enviar manda la confirmación con el token estático del custodio y el CUS cuando existe.
func (c clienteNucleo) Enviar(ctx context.Context, e Envio) error {
	cuerpo := map[string]any{
		"referencia":  e.Referencia,
		"estado":      e.Estado,
		"monto":       strconv.FormatInt(e.Monto, 10),
		"moneda":      e.Moneda,
		"ocurrido_en": e.OcurridoEn.UTC().Format(time.RFC3339Nano),
	}
	if e.CUS != "" {
		cuerpo["cus"] = e.CUS
	}
	return enviarJSON(ctx, c.cliente, c.url, c.token, cuerpo)
}

// clienteCustodio notifica el depósito a POST /notificaciones/depositos del simulador de custodia.
type clienteCustodio struct {
	url     string
	cliente *http.Client
}

// Enviar manda el movimiento con el monto en centavos y sin token.
func (c clienteCustodio) Enviar(ctx context.Context, e Envio) error {
	cuerpo := map[string]any{
		"referencia":     e.Referencia,
		"monto_centavos": strconv.FormatInt(e.Monto, 10),
		"ocurrido_en":    e.OcurridoEn.UTC().Format(time.RFC3339Nano),
	}
	return enviarJSON(ctx, c.cliente, c.url, "", cuerpo)
}

// enviarJSON hace el POST con el contexto, el token opcional y el rechazo de un status no 2xx.
func enviarJSON(ctx context.Context, cliente *http.Client, url, token string, cuerpo any) error {
	datos, err := json.Marshal(cuerpo)
	if err != nil {
		return fmt.Errorf("serializar cuerpo: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(datos))
	if err != nil {
		return fmt.Errorf("armar petición a %s: %w", redactarURL(url), causaSinURL(err))
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := cliente.Do(req)
	if err != nil {
		return fmt.Errorf("post a %s: %w", redactarURL(url), causaSinURL(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("post a %s: status %d", redactarURL(url), resp.StatusCode)
	}
	return nil
}

// causaSinURL quita de la causa el *url.Error que arrastraría el userinfo de la URL.
func causaSinURL(err error) error {
	var conURL *url.Error
	if errors.As(err, &conURL) {
		return conURL.Err
	}
	return err
}
