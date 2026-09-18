package directoriosim

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
)

// HU-NF-10, criterio 4: el adaptador pide la llave y devuelve la entidad y el producto.
func TestHU_NF_10_Criterio4_ResuelveEntidadYProducto(t *testing.T) {
	t.Parallel()

	var tipoRecibido, valorRecibido string
	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/resolver" {
			t.Errorf("ruta = %q, quiero /resolver", r.URL.Path)
		}
		tipoRecibido = r.URL.Query().Get("tipo")
		valorRecibido = r.URL.Query().Get("valor")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"entidad":"Banco Vecino","producto":"Cuenta de ahorros"}`))
	}))
	t.Cleanup(servidor.Close)

	directorio := NuevoDirectorio(servidor.URL, servidor.Client())
	resolucion, err := directorio.Resolver(t.Context(), nucleo.Llave{Tipo: nucleo.TipoLlaveCelular, Valor: "3001234567"})
	if err != nil {
		t.Fatalf("Resolver: %v", err)
	}
	if tipoRecibido != nucleo.TipoLlaveCelular || valorRecibido != "3001234567" {
		t.Errorf("el servidor recibió tipo=%q valor=%q, quiero el celular de la llave", tipoRecibido, valorRecibido)
	}
	if resolucion.Entidad != "Banco Vecino" || resolucion.Producto != "Cuenta de ahorros" {
		t.Errorf("resolución = %+v, quiero entidad y producto", resolucion)
	}
}

// HU-NF-10, criterio 5.1: un 404 del directorio es una llave inexistente, no un fallo temporal.
func TestHU_NF_10_Criterio5_1_CuatroCeroCuatroEsLlaveInexistente(t *testing.T) {
	t.Parallel()

	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusNotFound)
	}))
	t.Cleanup(servidor.Close)

	_, err := NuevoDirectorio(servidor.URL, servidor.Client()).Resolver(t.Context(), nucleo.Llave{Tipo: nucleo.TipoLlaveCorreo, Valor: "ana@ejemplo.com"})
	if !errors.Is(err, nucleo.ErrLlaveNoEncontrada) {
		t.Fatalf("Resolver = %v, quiero ErrLlaveNoEncontrada", err)
	}
	if errors.Is(err, nucleo.ErrDirectorioSinRespuesta) {
		t.Errorf("un 404 no es un error temporal: %v", err)
	}
}

// HU-NF-10, criterio 5.2: un directorio caído o lento da error temporal reintentable.
func TestHU_NF_10_Criterio5_2_DirectorioSinRespuestaEsTemporal(t *testing.T) {
	t.Parallel()

	t.Run("error del servidor", func(t *testing.T) {
		t.Parallel()

		servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		t.Cleanup(servidor.Close)

		_, err := NuevoDirectorio(servidor.URL, servidor.Client()).Resolver(t.Context(), nucleo.Llave{Tipo: nucleo.TipoLlaveCelular, Valor: "3001234567"})
		if !errors.Is(err, nucleo.ErrDirectorioSinRespuesta) || errors.Is(err, nucleo.ErrLlaveNoEncontrada) {
			t.Fatalf("Resolver = %v, quiero ErrDirectorioSinRespuesta y no ErrLlaveNoEncontrada", err)
		}
	})

	t.Run("servicio caído", func(t *testing.T) {
		t.Parallel()

		servidor := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := servidor.URL
		servidor.Close()

		_, err := NuevoDirectorio(url, nil).Resolver(t.Context(), nucleo.Llave{Tipo: nucleo.TipoLlaveCelular, Valor: "3001234567"})
		if !errors.Is(err, nucleo.ErrDirectorioSinRespuesta) {
			t.Fatalf("Resolver = %v, quiero ErrDirectorioSinRespuesta", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		t.Parallel()

		servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		t.Cleanup(servidor.Close)

		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer cancel()
		_, err := NuevoDirectorio(servidor.URL, servidor.Client()).Resolver(ctx, nucleo.Llave{Tipo: nucleo.TipoLlaveCelular, Valor: "3001234567"})
		if !errors.Is(err, nucleo.ErrDirectorioSinRespuesta) {
			t.Fatalf("Resolver = %v, quiero ErrDirectorioSinRespuesta", err)
		}
	})
}

// HU-NF-10, criterio 4: una respuesta con número de cuenta se rechaza; el contrato es entidad y producto.
func TestHU_NF_10_Criterio4_RespuestaConNumeroDeCuentaSeRechaza(t *testing.T) {
	t.Parallel()

	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"entidad":"Banco Vecino","producto":"Cuenta de ahorros","numero_cuenta":"1234567890"}`))
	}))
	t.Cleanup(servidor.Close)

	_, err := NuevoDirectorio(servidor.URL, servidor.Client()).Resolver(t.Context(), nucleo.Llave{Tipo: nucleo.TipoLlaveCelular, Valor: "3001234567"})
	if !errors.Is(err, nucleo.ErrDirectorioSinRespuesta) {
		t.Fatalf("Resolver = %v, quiero un error por el campo ajeno numero_cuenta", err)
	}
}
