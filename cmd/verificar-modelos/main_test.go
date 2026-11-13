package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// raizVoz es el nombre de la raíz de los pesos del contenedor de voz.
const raizVoz = "voz"

// artefactoDePrueba arma un artefacto completo a partir de un contenido que aún no existe.
func artefactoDePrueba(nombre, raiz, ruta string, contenido []byte) Artefacto {
	suma := sha256.Sum256(contenido)
	return Artefacto{
		Nombre:             nombre,
		Raiz:               raiz,
		Origen:             "prueba unitaria",
		Ruta:               ruta,
		Sha256:             hex.EncodeToString(suma[:]),
		Bytes:              int64(len(contenido)),
		Licencia:           "Apache-2.0",
		FechaCaptura:       "2026-11-10",
		VersionHerramienta: "prueba 1.0",
	}
}

// escribir escribe el contenido bajo la raíz y devuelve su ruta.
func escribir(t *testing.T, raiz, ruta string, contenido []byte) {
	t.Helper()
	destino := filepath.Join(raiz, filepath.FromSlash(ruta))
	if err := os.MkdirAll(filepath.Dir(destino), 0o755); err != nil {
		t.Fatalf("crear el directorio de %s: %v", ruta, err)
	}
	if err := os.WriteFile(destino, contenido, 0o644); err != nil {
		t.Fatalf("escribir %s: %v", ruta, err)
	}
}

// escribirLock serializa el bloqueo como lockfile.
func escribirLock(t *testing.T, carpeta string, bloqueo Bloqueo) string {
	t.Helper()
	crudo, err := json.MarshalIndent(bloqueo, "", "  ")
	if err != nil {
		t.Fatalf("serializar el lockfile: %v", err)
	}
	ruta := filepath.Join(carpeta, "lock.json")
	if err := os.WriteFile(ruta, crudo, 0o644); err != nil {
		t.Fatalf("escribir el lockfile: %v", err)
	}
	return ruta
}

// montar escribe los pesos y el lockfile en un directorio temporal y devuelve ambas rutas.
func montar(t *testing.T, artefactos []Artefacto, contenidos map[string][]byte) (string, string) {
	t.Helper()
	carpeta := t.TempDir()
	raiz := filepath.Join(carpeta, "pesos")
	if err := os.MkdirAll(raiz, 0o755); err != nil {
		t.Fatalf("crear la raíz: %v", err)
	}
	for ruta, contenido := range contenidos {
		escribir(t, raiz, ruta, contenido)
	}
	rutaLock := escribirLock(t, carpeta, Bloqueo{
		Version:    VersionDelLock,
		Raices:     map[string]string{"local": "pesos"},
		Artefactos: artefactos,
	})
	return rutaLock, raiz
}

func TestEjecutarPasaConElConjuntoCorrecto(t *testing.T) {
	voz := []byte("pesos de la voz")
	modelo := []byte("pesos del modelo")
	rutaLock, raiz := montar(t, []Artefacto{
		artefactoDePrueba("voz", "local", "kokoro/voices.bin", voz),
		artefactoDePrueba("modelo", "local", "gemma4/modelo.gguf", modelo),
	}, map[string][]byte{
		"kokoro/voices.bin":  voz,
		"gemma4/modelo.gguf": modelo,
	})

	var salida bytes.Buffer
	fallos, err := ejecutar(rutaLock, nil, false, &salida)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if len(fallos) != 0 {
		t.Fatalf("se esperaban cero fallos y hubo %d: %v", len(fallos), fallos)
	}
	if !strings.Contains(salida.String(), "Verificados 2") {
		t.Fatalf("el informe no cuenta los dos artefactos: %q", salida.String())
	}
	if !strings.Contains(salida.String(), raiz) {
		t.Fatalf("el informe no nombra la raíz verificada: %q", salida.String())
	}
}

func TestEjecutarFallaConUnByteAlterado(t *testing.T) {
	original := []byte("pesos correctos del modelo")
	alterado := append([]byte(nil), original...)
	alterado[0] = 'P'
	if len(original) != len(alterado) {
		t.Fatalf("la prueba necesita el mismo tamaño para cambiar un solo byte")
	}
	rutaLock, raiz := montar(t, []Artefacto{
		artefactoDePrueba("modelo", "local", "gemma4/modelo.gguf", original),
	}, map[string][]byte{
		"gemma4/modelo.gguf": alterado,
	})

	fallos, err := ejecutar(rutaLock, nil, false, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if len(fallos) != 1 {
		t.Fatalf("se esperaba un fallo de huella y hubo %d: %v", len(fallos), fallos)
	}
	if !strings.Contains(fallos[0], "no coincide") {
		t.Fatalf("el fallo no explica la huella: %q", fallos[0])
	}
	if !strings.Contains(fallos[0], raiz) {
		t.Fatalf("el fallo no nombra el archivo revisado: %q", fallos[0])
	}
}

func TestEjecutarFallaConUnArchivoFaltante(t *testing.T) {
	presente := []byte("pesos que sí están")
	ausente := []byte("pesos que no están")
	rutaLock, _ := montar(t, []Artefacto{
		artefactoDePrueba("presente", "local", "kokoro/voices.bin", presente),
		artefactoDePrueba("ausente", "local", "gemma4/modelo.gguf", ausente),
	}, map[string][]byte{
		"kokoro/voices.bin": presente,
	})

	fallos, err := ejecutar(rutaLock, nil, false, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if len(fallos) != 1 {
		t.Fatalf("se esperaba un fallo por archivo ausente y hubo %d: %v", len(fallos), fallos)
	}
	if !strings.Contains(fallos[0], "no se puede leer") {
		t.Fatalf("el fallo no menciona el archivo ausente: %q", fallos[0])
	}
}

func TestEjecutarReportaTodosLosFallosJuntos(t *testing.T) {
	correcto := []byte("pesos correctos")
	dosVeces := []byte("pesos alterados")
	alterado := []byte("pesos alteradOs")
	if len(dosVeces) != len(alterado) {
		t.Fatalf("la prueba necesita el mismo tamaño")
	}
	rutaLock, _ := montar(t, []Artefacto{
		artefactoDePrueba("correcto", "local", "a.bin", correcto),
		artefactoDePrueba("alterado", "local", "b.bin", dosVeces),
		artefactoDePrueba("ausente", "local", "c.bin", correcto),
	}, map[string][]byte{
		"a.bin": correcto,
		"b.bin": alterado,
	})

	fallos, err := ejecutar(rutaLock, nil, false, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if len(fallos) != 2 {
		t.Fatalf("se esperaban dos fallos acumulados y hubo %d: %v", len(fallos), fallos)
	}
}

func TestEjecutarConSoloLockNoTocaLosDiscos(t *testing.T) {
	carpeta := t.TempDir()
	rutaLock := escribirLock(t, carpeta, Bloqueo{
		Version: VersionDelLock,
		Raices:  map[string]string{"local": "pesos-que-no-existen"},
		Artefactos: []Artefacto{
			artefactoDePrueba("modelo", "local", "gemma4/modelo.gguf", []byte("da igual")),
			{
				Nombre:             "whisper-small",
				Origen:             "descarga automática",
				Ruta:               "whisper/small",
				Pin:                "faster-whisper==1.2.1, modelo small",
				Licencia:           "MIT",
				FechaCaptura:       "2026-11-10",
				VersionHerramienta: "faster-whisper 1.2.1",
			},
		},
	})

	var salida bytes.Buffer
	fallos, err := ejecutar(rutaLock, map[string]string{"local": filepath.Join(carpeta, "no-existe")}, true, &salida)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if len(fallos) != 0 {
		t.Fatalf("-solo-lock no debía mirar los discos: %v", fallos)
	}
	if !strings.Contains(salida.String(), "No se revisaron archivos") {
		t.Fatalf("el informe no aclara que no se revisaron archivos: %q", salida.String())
	}
}

func TestEjecutarConUnPinNoExigeArchivo(t *testing.T) {
	carpeta := t.TempDir()
	raiz := filepath.Join(carpeta, "pesos")
	if err := os.MkdirAll(raiz, 0o755); err != nil {
		t.Fatalf("crear la raíz: %v", err)
	}
	rutaLock := escribirLock(t, carpeta, Bloqueo{
		Version: VersionDelLock,
		Raices:  map[string]string{"local": "pesos"},
		Artefactos: []Artefacto{
			{
				Nombre:             "whisper-small",
				Origen:             "descarga automática",
				Ruta:               "whisper/small",
				Pin:                "faster-whisper==1.2.1, modelo small",
				Licencia:           "MIT",
				FechaCaptura:       "2026-11-10",
				VersionHerramienta: "faster-whisper 1.2.1",
			},
		},
	})

	var salida bytes.Buffer
	fallos, err := ejecutar(rutaLock, nil, false, &salida)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if len(fallos) != 0 {
		t.Fatalf("un artefacto fijado por pin no se compara con el disco: %v", fallos)
	}
	if !strings.Contains(salida.String(), "fijado por pin") {
		t.Fatalf("el informe no menciona el pin: %q", salida.String())
	}
}

func TestEjecutarResuelveLaRaizDelLockfile(t *testing.T) {
	carpeta := t.TempDir()
	contenido := []byte("pesos relativos al lockfile")
	escribir(t, carpeta, "pesos/recurso.bin", contenido)

	subcarpeta := filepath.Join(carpeta, "config")
	if err := os.MkdirAll(subcarpeta, 0o755); err != nil {
		t.Fatalf("crear la subcarpeta: %v", err)
	}
	rutaLock := escribirLock(t, subcarpeta, Bloqueo{
		Version:    VersionDelLock,
		Raices:     map[string]string{"local": "../pesos"},
		Artefactos: []Artefacto{artefactoDePrueba("recurso", "local", "recurso.bin", contenido)},
	})

	fallos, err := ejecutar(rutaLock, nil, false, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if len(fallos) != 0 {
		t.Fatalf("la raíz relativa al lockfile debía resolver sin fallos: %v", fallos)
	}
}

func TestEjecutarVerificaCadaRaizDeclarada(t *testing.T) {
	carpeta := t.TempDir()
	gguf := []byte("pesos del GGUF")
	voz := []byte("pesos de la voz")
	raizGGUF := filepath.Join(carpeta, "modelos")
	raizVozLocal := filepath.Join(carpeta, "voz")
	escribir(t, raizGGUF, "gemma4/modelo.gguf", gguf)
	escribir(t, raizVozLocal, "kokoro-v1.0.int8.onnx", voz)

	rutaLock := escribirLock(t, carpeta, Bloqueo{
		Version: VersionDelLock,
		Raices: map[string]string{
			"gguf":  "modelos",
			raizVoz: filepath.ToSlash(raizVozLocal),
		},
		Artefactos: []Artefacto{
			artefactoDePrueba("gemma", "gguf", "gemma4/modelo.gguf", gguf),
			artefactoDePrueba("kokoro", raizVoz, "kokoro-v1.0.int8.onnx", voz),
		},
	})

	var salida bytes.Buffer
	fallos, err := ejecutar(rutaLock, nil, false, &salida)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if len(fallos) != 0 {
		t.Fatalf("las dos raíces debían resolver sin fallos: %v", fallos)
	}
	for _, esperada := range []string{raizGGUF, raizVozLocal} {
		if !strings.Contains(salida.String(), esperada) {
			t.Fatalf("el informe no nombra la raíz %s: %q", esperada, salida.String())
		}
	}
}

func TestEjecutarSobreescribeUnaRaizPorNombre(t *testing.T) {
	carpeta := t.TempDir()
	contenido := []byte("pesos copiados a otro sitio")
	otraRaiz := filepath.Join(carpeta, "otro-disco")
	escribir(t, otraRaiz, "gemma4/modelo.gguf", contenido)

	rutaLock := escribirLock(t, carpeta, Bloqueo{
		Version:    VersionDelLock,
		Raices:     map[string]string{"local": "pesos-que-no-existen"},
		Artefactos: []Artefacto{artefactoDePrueba("modelo", "local", "gemma4/modelo.gguf", contenido)},
	})

	var salida bytes.Buffer
	fallos, err := ejecutar(rutaLock, map[string]string{"local": otraRaiz}, false, &salida)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if len(fallos) != 0 {
		t.Fatalf("-raiz debía resolver la raíz nombrada y no hubo caso: %v", fallos)
	}
	if !strings.Contains(salida.String(), otraRaiz) {
		t.Fatalf("el informe no usa la raíz sobreescrita: %q", salida.String())
	}
}

func TestEjecutarConUnaRaizSueltaReemplazaTodas(t *testing.T) {
	carpeta := t.TempDir()
	contenido := []byte("pesos del árbol copiado")
	copiada := filepath.Join(carpeta, "copia")
	escribir(t, copiada, "gemma4/modelo.gguf", contenido)

	rutaLock := escribirLock(t, carpeta, Bloqueo{
		Version:    VersionDelLock,
		Raices:     map[string]string{"local": "pesos-que-no-existen"},
		Artefactos: []Artefacto{artefactoDePrueba("modelo", "local", "gemma4/modelo.gguf", contenido)},
	})

	fallos, err := ejecutar(rutaLock, map[string]string{"": copiada}, false, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if len(fallos) != 0 {
		t.Fatalf("una -raiz suelta debía reemplazar todas las raíces: %v", fallos)
	}
}

func TestEjecutarRechazaUnaRaizQueElLockfileNoDeclara(t *testing.T) {
	carpeta := t.TempDir()
	contenido := []byte("pesos del modelo")
	escribir(t, carpeta, "pesos/a.bin", contenido)

	rutaLock := escribirLock(t, carpeta, Bloqueo{
		Version:    VersionDelLock,
		Raices:     map[string]string{"local": "pesos"},
		Artefactos: []Artefacto{artefactoDePrueba("modelo", "local", "a.bin", contenido)},
	})

	fallos, err := ejecutar(rutaLock, map[string]string{"otra": carpeta}, false, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if len(fallos) != 1 {
		t.Fatalf("se esperaba un fallo por la raíz desconocida y hubo %d: %v", len(fallos), fallos)
	}
	if !strings.Contains(fallos[0], "no declara esa raíz") {
		t.Fatalf("el fallo no explica la raíz sobrante: %q", fallos[0])
	}
}

func TestEjecutarFallaConUnLockfileInexistente(t *testing.T) {
	if _, err := ejecutar(filepath.Join(t.TempDir(), "no-existe.json"), nil, true, &bytes.Buffer{}); err == nil {
		t.Fatal("un lockfile ausente debe ser error y no una lista de fallos vacía")
	}
}

func TestValidarEstructuraRechazaLockfilesMalformados(t *testing.T) {
	base := func() Bloqueo {
		return Bloqueo{
			Version: VersionDelLock,
			Raices:  map[string]string{"local": "pesos"},
			Artefactos: []Artefacto{
				artefactoDePrueba("modelo", "local", "gemma4/modelo.gguf", []byte("pesos")),
			},
		}
	}

	casos := []struct {
		nombre string
		tocado func(*Bloqueo)
		espera string
	}{
		{"versión desconocida", func(b *Bloqueo) { b.Version = 99 }, "version"},
		{"sin raíces", func(b *Bloqueo) { b.Raices = nil }, "no declara ninguna"},
		{"raíz sin ruta", func(b *Bloqueo) { b.Raices = map[string]string{"local": " "} }, "falta la ruta"},
		{"raíz sin normalizar", func(b *Bloqueo) { b.Raices = map[string]string{"local": "pesos/"} }, "normalizada"},
		{"sin artefactos", func(b *Bloqueo) { b.Artefactos = nil }, "no declara ninguno"},
		{"nombre repetido", func(b *Bloqueo) { b.Artefactos = append(b.Artefactos, b.Artefactos[0]) }, "repetido"},
		{"sin origen", func(b *Bloqueo) { b.Artefactos[0].Origen = " " }, "falta origen"},
		{"sin licencia", func(b *Bloqueo) { b.Artefactos[0].Licencia = "" }, "falta licencia"},
		{"sin herramienta", func(b *Bloqueo) { b.Artefactos[0].VersionHerramienta = "" }, "falta version_herramienta"},
		{"fecha inválida", func(b *Bloqueo) { b.Artefactos[0].FechaCaptura = "10/11/2026" }, "fecha_captura"},
		{"huella corta", func(b *Bloqueo) { b.Artefactos[0].Sha256 = "abcd" }, "sha256"},
		{"huella en mayúsculas", func(b *Bloqueo) {
			b.Artefactos[0].Sha256 = strings.ToUpper(b.Artefactos[0].Sha256)
		}, "sha256"},
		{"huella y pin", func(b *Bloqueo) { b.Artefactos[0].Pin = "algo" }, "a la vez"},
		{"ni huella ni pin", func(b *Bloqueo) { b.Artefactos[0].Sha256 = "" }, "falta sha256 o pin"},
		{"bytes no positivos", func(b *Bloqueo) { b.Artefactos[0].Bytes = 0 }, "bytes"},
		{"sin raíz en el artefacto", func(b *Bloqueo) { b.Artefactos[0].Raiz = "" }, "falta raiz"},
		{"raíz inexistente", func(b *Bloqueo) { b.Artefactos[0].Raiz = "otra" }, "no está declarada"},
		{"raíz con forma de ruta", func(b *Bloqueo) { b.Artefactos[0].Raiz = "local/pesos" }, "no una ruta"},
		{"ruta absoluta", func(b *Bloqueo) { b.Artefactos[0].Ruta = "/etc/passwd" }, "absoluta"},
		{"ruta con escape", func(b *Bloqueo) { b.Artefactos[0].Ruta = "../secreto.bin" }, "salir de la raíz"},
		{"ruta sin normalizar", func(b *Bloqueo) { b.Artefactos[0].Ruta = "gemma4//modelo.gguf" }, "normalizada"},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			bloqueo := base()
			caso.tocado(&bloqueo)
			fallos := validarEstructura(bloqueo)
			if len(fallos) == 0 {
				t.Fatalf("se esperaba un fallo que mencionara %q", caso.espera)
			}
			if !strings.Contains(strings.Join(fallos, "\n"), caso.espera) {
				t.Fatalf("los fallos no mencionan %q: %v", caso.espera, fallos)
			}
		})
	}
}

func TestLeerBloqueoRechazaJSONInvalidoYClavesDesconocidas(t *testing.T) {
	casos := map[string]string{
		"json truncado":   `{"version": 1, "artefactos": [`,
		"clave inventada": `{"version": 1, "artefactos": [], "pesos": []}`,
		"tipo equivocado": `{"version": "uno", "artefactos": []}`,
	}

	for nombre, contenido := range casos {
		t.Run(nombre, func(t *testing.T) {
			ruta := filepath.Join(t.TempDir(), "lock.json")
			if err := os.WriteFile(ruta, []byte(contenido), 0o644); err != nil {
				t.Fatalf("escribir el lockfile: %v", err)
			}
			if _, err := leerBloqueo(ruta); err == nil {
				t.Fatal("se esperaba un error de parseo")
			}
		})
	}
}

func TestHuellaValida(t *testing.T) {
	suma := sha256.Sum256([]byte("contenido"))
	buena := hex.EncodeToString(suma[:])
	if !huellaValida(buena) {
		t.Fatalf("una huella real debía aceptarse: %s", buena)
	}
	for _, mala := range []string{"", "abc", strings.ToUpper(buena), buena + "0", strings.Repeat("z", 64)} {
		if huellaValida(mala) {
			t.Fatalf("una huella inválida debía rechazarse: %q", mala)
		}
	}
}

func TestLockfileDelRepositorioEsValido(t *testing.T) {
	ruta := filepath.Join("..", "..", "modelos", "lock.json")
	var salida bytes.Buffer
	fallos, err := ejecutar(ruta, nil, true, &salida)
	if err != nil {
		t.Fatalf("el lockfile versionado no se pudo leer: %v", err)
	}
	if len(fallos) != 0 {
		t.Fatalf("el lockfile versionado no es válido: %v", fallos)
	}
}

// TestLockfileCoincideConElManifiestoDeVoz cruza el lockfile con el manifiesto que usa el build de
// infra/voz-local. Son dos fuentes con los mismos sha256 y tamaños: si una cambia y la otra no, la
// prueba falla en vez de dejar que el despliegue sirva un archivo distinto.
func TestLockfileCoincideConElManifiestoDeVoz(t *testing.T) {
	crudo, err := os.ReadFile(filepath.Join("..", "..", "infra", "voz-local", "descargar.py"))
	if err != nil {
		t.Fatalf("leer el manifiesto de voz: %v", err)
	}
	patron := regexp.MustCompile(`"([^"]+)"\s*:\s*\(\s*"([0-9a-f]{64})"\s*,\s*(\d+)\s*,?\s*\)`)
	manifiesto := patron.FindAllStringSubmatch(string(crudo), -1)
	if len(manifiesto) == 0 {
		t.Fatal("no se encontró ningún par de huella y tamaño en infra/voz-local/descargar.py")
	}

	bloqueo, err := leerBloqueo(filepath.Join("..", "..", "modelos", "lock.json"))
	if err != nil {
		t.Fatalf("leer el lockfile versionado: %v", err)
	}

	porArchivo := make(map[string]Artefacto, len(bloqueo.Artefactos))
	for _, artefacto := range bloqueo.Artefactos {
		if artefacto.Sha256 == "" {
			continue
		}
		porArchivo[filepath.Base(filepath.FromSlash(artefacto.Ruta))] = artefacto
	}

	delManifiesto := make(map[string]bool, len(manifiesto))
	for _, encontrado := range manifiesto {
		archivo, huella, tamano := encontrado[1], encontrado[2], encontrado[3]
		delManifiesto[archivo] = true
		artefacto, hay := porArchivo[archivo]
		if !hay {
			t.Errorf("el manifiesto declara %s y ningún artefacto del lockfile apunta a ese archivo", archivo)
			continue
		}
		if artefacto.Sha256 != huella {
			t.Errorf("%s: el lockfile declara %s y el manifiesto %s", archivo, artefacto.Sha256, huella)
		}
		if strconv.FormatInt(artefacto.Bytes, 10) != tamano {
			t.Errorf("%s: el lockfile declara %d bytes y el manifiesto %s", archivo, artefacto.Bytes, tamano)
		}
	}

	conRaizVoz := 0
	for archivo, artefacto := range porArchivo {
		if artefacto.Raiz != raizVoz {
			continue
		}
		conRaizVoz++
		if !delManifiesto[archivo] {
			t.Errorf("%s: el artefacto usa la raíz %s y el manifiesto no lo descarga", archivo, raizVoz)
		}
	}
	if conRaizVoz != len(manifiesto) {
		t.Fatalf("el lockfile declara %d artefactos en la raíz %s y el manifiesto %d archivos",
			conRaizVoz, raizVoz, len(manifiesto))
	}
}
