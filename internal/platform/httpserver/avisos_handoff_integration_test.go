//go:build integration

package httpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

func TestAvisosHandoffHTTPReal(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	usuario, otro := uuid.NewString(), uuid.NewString()
	for i := range 5 {
		_, e := pool.Exec(t.Context(), `INSERT INTO inteligencia.aviso(id,usuario_id,tipo,clave,periodo,severidad_centavos,prioridad,texto) VALUES($1,$2,'cobro_nuevo',$3,$4::date,100,1,'aviso HTTP')`, uuid.New(), usuario, fmt.Sprintf("empate|%d", i/2), fmt.Sprintf("2026-07-%02d", i+1))
		if e != nil {
			t.Fatal(e)
		}
	}
	handler := inteligencia.NuevoHandler(inteligencia.NuevoServicio(pool))
	montar := func(u string) *httptest.Server {
		return httptest.NewServer(NewRouterConVera(pool, nucleo.NewServicio(pool, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{Identidad: identidadDeContextoFijo{id: identidad.Identidad{UsuarioID: u, ParteID: u}}, Inteligencia: handler}, DependenciasVera{}))
	}
	dueno := montar(usuario)
	defer dueno.Close()
	ajeno := montar(otro)
	defer ajeno.Close()
	vistos := map[uuid.UUID]bool{}
	ruta := "/v1/inteligencia/avisos?limite=2"
	for {
		res, e := dueno.Client().Get(dueno.URL + ruta)
		if e != nil {
			t.Fatal(e)
		}
		var env struct {
			Data []api.InteligenciaAviso `json:"data"`
			Meta any                     `json:"meta"`
		}
		e = json.NewDecoder(res.Body).Decode(&env)
		_ = res.Body.Close()
		if e != nil || res.StatusCode != 200 || env.Meta != nil {
			t.Fatal(res.StatusCode, e)
		}
		for _, a := range env.Data {
			if vistos[a.Id] {
				t.Fatal("aviso repetido por HTTP")
			}
			vistos[a.Id] = true
		}
		cursor := res.Header.Get("X-Vera-Siguiente-Cursor")
		if cursor == "" {
			break
		}
		ruta = "/v1/inteligencia/avisos?limite=2&cursor=" + cursor
	}
	if len(vistos) != 5 {
		t.Fatal("avisos omitidos por HTTP")
	}

	pedir := func(srv *httptest.Server, metodo, ruta string) (int, string) {
		t.Helper()
		r, e := http.NewRequestWithContext(t.Context(), metodo, srv.URL+ruta, nil)
		if e != nil {
			t.Fatal(e)
		}
		res, e := srv.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		b, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		return res.StatusCode, string(b)
	}
	for id := range vistos {
		codigo, _ := pedir(dueno, "GET", "/v1/inteligencia/avisos/"+id.String())
		if codigo != 200 {
			t.Fatal(codigo)
		}
		var esperado string
		for _, suffix := range []string{"", "/descartar"} {
			metodo := "GET"
			if suffix != "" {
				metodo = "POST"
			}
			for _, identificador := range []string{id.String(), uuid.NewString()} {
				codigo, cuerpo := pedir(ajeno, metodo, "/v1/inteligencia/avisos/"+identificador+suffix)
				if codigo != 404 {
					t.Fatal(codigo, cuerpo)
				}
				if esperado != "" && cuerpo != esperado {
					t.Fatal("404 diferentes")
				}
				esperado = cuerpo
			}
		}
		codigo, _ = pedir(dueno, "GET", "/v1/inteligencia/avisos/"+id.String())
		if codigo != 200 {
			t.Fatal("descarte ajeno modificó la tarjeta")
		}
		break
	}
	for _, query := range []string{"limite=0", "limite=51", "limite=abc", "estado=otro", "cursor=invalido", "mes=2026-07&limite=2"} {
		codigo, cuerpo := pedir(dueno, "GET", "/v1/inteligencia/avisos?"+query)
		if codigo != 400 {
			t.Fatal(query, codigo, cuerpo)
		}
	}
}
