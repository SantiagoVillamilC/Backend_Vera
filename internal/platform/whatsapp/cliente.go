// Package whatsapp es el canal de OTP por WhatsApp Cloud API.
package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
)

// BaseGraphAPI es el endpoint de producción de WhatsApp Cloud API.
const BaseGraphAPI = "https://graph.facebook.com/v23.0"

// tiempoEsperaWhatsApp es el plazo propio del envío a Cloud API.
const tiempoEsperaWhatsApp = 5 * time.Second

// topeCuerpoWhatsApp es el tamaño máximo del cuerpo que el cliente acepta de Cloud API.
const topeCuerpoWhatsApp = 64 * 1024

// idiomaPlantilla es el código de idioma con que se aprueba la plantilla de autenticación.
const idiomaPlantilla = "es"

// Cliente entrega códigos OTP con la plantilla de autenticación de WhatsApp Cloud API.
type Cliente struct {
	base      string
	token     string
	phoneID   string
	plantilla string
	cliente   *http.Client
}

// Nuevo arma el cliente con la base, las credenciales, la plantilla y un cliente HTTP; sin cliente usa uno con plazo.
func Nuevo(base, token, phoneID, plantilla string, cliente *http.Client) *Cliente {
	if cliente == nil {
		cliente = &http.Client{Timeout: tiempoEsperaWhatsApp}
	}
	return &Cliente{base: strings.TrimRight(base, "/"), token: token, phoneID: phoneID, plantilla: plantilla, cliente: cliente}
}

// Enviar publica el código como parámetro de la plantilla y devuelve el id del mensaje aceptado.
func (c *Cliente) Enviar(ctx context.Context, celularE164, codigo string) (string, error) {
	datos, err := json.Marshal(c.cuerpoDePlantilla(celularE164, codigo))
	if err != nil {
		return "", fmt.Errorf("whatsapp: serializar la petición: %w", err)
	}
	ruta := c.base + "/" + url.PathEscape(c.phoneID) + "/messages"
	peticion, err := http.NewRequestWithContext(ctx, http.MethodPost, ruta, bytes.NewReader(datos))
	if err != nil {
		return "", fmt.Errorf("whatsapp: armar la petición: %w", causaSinURL(err))
	}
	peticion.Header.Set("Authorization", "Bearer "+c.token)
	peticion.Header.Set("Content-Type", "application/json")
	respuesta, err := c.cliente.Do(peticion)
	if err != nil {
		return "", fmt.Errorf("whatsapp: enviar el mensaje: %w", causaSinURL(err))
	}
	defer func() { _ = respuesta.Body.Close() }()
	if respuesta.StatusCode < 200 || respuesta.StatusCode >= 300 {
		slog.WarnContext(ctx, "whatsapp: Cloud API rechazó el envío", "status", respuesta.StatusCode)
		return "", fmt.Errorf("whatsapp: Cloud API respondió %s", respuesta.Status)
	}
	var salida respuestaEnvio
	if err := leerRespuesta(respuesta.Body, &salida); err != nil {
		return "", err
	}
	if len(salida.Messages) == 0 || salida.Messages[0].ID == "" {
		return "", errors.New("whatsapp: Cloud API respondió sin id de mensaje")
	}
	id := salida.Messages[0].ID
	slog.InfoContext(ctx, "whatsapp: mensaje enviado", "id_mensaje", id, "status", respuesta.StatusCode)
	return id, nil
}

// cuerpoDePlantilla arma el cuerpo documentado por Meta para una plantilla de autenticación.
func (c *Cliente) cuerpoDePlantilla(celularE164, codigo string) cuerpoEnvio {
	parametro := cuerpoParametro{Type: "text", Text: codigo}
	return cuerpoEnvio{
		MessagingProduct: "whatsapp",
		To:               strings.TrimPrefix(celularE164, "+"),
		Type:             "template",
		Template: cuerpoPlantilla{
			Name:     c.plantilla,
			Language: cuerpoIdioma{Code: idiomaPlantilla},
			Components: []cuerpoComponente{
				{Type: "body", Parameters: []cuerpoParametro{parametro}},
				{Type: "button", SubType: "url", Index: "0", Parameters: []cuerpoParametro{parametro}},
			},
		},
	}
}

// cuerpoEnvio es el cuerpo del POST a /messages de la Graph API.
type cuerpoEnvio struct {
	MessagingProduct string          `json:"messaging_product"`
	To               string          `json:"to"`
	Type             string          `json:"type"`
	Template         cuerpoPlantilla `json:"template"`
}

// cuerpoPlantilla nombra la plantilla aprobada y sus parámetros.
type cuerpoPlantilla struct {
	Name       string             `json:"name"`
	Language   cuerpoIdioma       `json:"language"`
	Components []cuerpoComponente `json:"components"`
}

// cuerpoIdioma fija el idioma de la plantilla.
type cuerpoIdioma struct {
	Code string `json:"code"`
}

// cuerpoComponente es un componente de la plantilla con sus parámetros.
type cuerpoComponente struct {
	Type       string            `json:"type"`
	SubType    string            `json:"sub_type,omitempty"`
	Index      string            `json:"index,omitempty"`
	Parameters []cuerpoParametro `json:"parameters"`
}

// cuerpoParametro es un parámetro de texto del componente.
type cuerpoParametro struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// respuestaEnvio es la respuesta de Cloud API con el id del mensaje aceptado.
type respuestaEnvio struct {
	Messages []mensajeAceptado `json:"messages"`
}

// mensajeAceptado identifica el mensaje que la Graph API aceptó.
type mensajeAceptado struct {
	ID string `json:"id"`
}

// leerRespuesta decodifica el cuerpo acotado de Cloud API.
func leerRespuesta(cuerpo io.Reader, destino *respuestaEnvio) error {
	datos, err := io.ReadAll(io.LimitReader(cuerpo, topeCuerpoWhatsApp+1))
	if err != nil {
		return fmt.Errorf("whatsapp: leer la respuesta: %w", err)
	}
	if len(datos) > topeCuerpoWhatsApp {
		return errors.New("whatsapp: la respuesta de Cloud API superó el tamaño máximo")
	}
	if err := json.Unmarshal(datos, destino); err != nil {
		return fmt.Errorf("whatsapp: leer la respuesta: %w", err)
	}
	return nil
}

// causaSinURL quita de la causa el *url.Error que arrastraría la URL de la petición.
func causaSinURL(err error) error {
	var conURL *url.Error
	if errors.As(err, &conURL) {
		return conURL.Err
	}
	return err
}

var _ auth.CanalOTP = (*Cliente)(nil)
