// Comando verificar-modelos comprueba el lockfile de artefactos de modelos contra los pesos reales.
//
// Los pesos no viven en el repositorio, así que el lockfile es lo único que fija qué archivo
// sirve cada despliegue. Con -solo-lock se revisa la estructura sin tocar discos, que es lo que
// corre en CI. Sin esa bandera además se compara cada archivo declarado con su ruta.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// VersionDelLock es la única versión del formato que este verificador entiende.
	VersionDelLock = 1

	// RutaDelLockPorDefecto es el lockfile versionado del repositorio.
	RutaDelLockPorDefecto = "modelos/lock.json"

	// FormatoFecha es el formato exigido a fecha_captura.
	FormatoFecha = "2006-01-02"
)

// Bloqueo es el contenido de modelos/lock.json. Raices nombra los directorios de modelos: los GGUF
// locales y los pesos del contenedor de voz no cuelgan del mismo árbol, así que cada artefacto
// elige una raíz por nombre.
type Bloqueo struct {
	Version    int               `json:"version"`
	Raices     map[string]string `json:"raices"`
	Artefactos []Artefacto       `json:"artefactos"`
}

// Artefacto es un peso declarado. Otra por sha256 y bytes, o por pin cuando no hay archivo
// verificable, como el modelo de Whisper que descarga faster-whisper.
type Artefacto struct {
	Nombre             string `json:"nombre"`
	Raiz               string `json:"raiz,omitempty"`
	Origen             string `json:"origen"`
	Ruta               string `json:"ruta"`
	Sha256             string `json:"sha256,omitempty"`
	Bytes              int64  `json:"bytes,omitempty"`
	Pin                string `json:"pin,omitempty"`
	Licencia           string `json:"licencia"`
	FechaCaptura       string `json:"fecha_captura"`
	VersionHerramienta string `json:"version_herramienta"`
}

// raicesSobreescritas acumula -raiz repetido. La forma NOMBRE=RUTA reemplaza esa raíz concreta.
// Una ruta suelta reemplaza todas, que es lo útil cuando el árbol completo se copió a otro sitio.
type raicesSobreescritas map[string]string

func (r raicesSobreescritas) String() string {
	nombres := make([]string, 0, len(r))
	for nombre := range r {
		nombres = append(nombres, nombre)
	}
	sort.Strings(nombres)
	partes := make([]string, 0, len(nombres))
	for _, nombre := range nombres {
		if nombre == "" {
			partes = append(partes, r[nombre])
			continue
		}
		partes = append(partes, nombre+"="+r[nombre])
	}
	return strings.Join(partes, " ")
}

func (r raicesSobreescritas) Set(valor string) error {
	nombre, ruta, hayIgual := strings.Cut(valor, "=")
	if !hayIgual {
		if strings.TrimSpace(valor) == "" {
			return errors.New("-raiz necesita una ruta")
		}
		r[""] = strings.TrimSpace(valor)
		return nil
	}
	nombre = strings.TrimSpace(nombre)
	ruta = strings.TrimSpace(ruta)
	if nombre == "" || ruta == "" {
		return fmt.Errorf("-raiz %q necesita el formato NOMBRE=RUTA", valor)
	}
	r[nombre] = ruta
	return nil
}

func main() {
	rutaLock := flag.String("lock", RutaDelLockPorDefecto, "ruta del lockfile de artefactos")
	sobreescritas := raicesSobreescritas{}
	flag.Var(sobreescritas, "raiz", "raíz de modelos que reemplaza a las del lockfile. Repetible: RUTA reemplaza todas, NOMBRE=RUTA reemplaza una")
	soloLock := flag.Bool("solo-lock", false, "valida solo la estructura del lockfile, sin tocar los discos")
	flag.Parse()

	fallos, err := ejecutar(*rutaLock, sobreescritas, *soloLock, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "verificar-modelos:", err)
		os.Exit(1)
	}
	if len(fallos) > 0 {
		for _, fallo := range fallos {
			fmt.Fprintln(os.Stderr, "fallo:", fallo)
		}
		fmt.Fprintf(os.Stderr, "verificar-modelos: %d fallos.\n", len(fallos))
		os.Exit(1)
	}
}

// ejecutar devuelve los fallos encontrados. Un error solo aparece cuando no se puede ni empezar,
// por ejemplo un lockfile ausente o un JSON que no parsea.
func ejecutar(rutaLock string, sobreescritas map[string]string, soloLock bool, salida io.Writer) ([]string, error) {
	bloqueo, err := leerBloqueo(rutaLock)
	if err != nil {
		return nil, err
	}

	if fallos := validarEstructura(bloqueo); len(fallos) > 0 {
		return fallos, nil
	}

	if soloLock {
		fmt.Fprintf(salida, "Lockfile válido: %d artefactos, %d con huella y %d por pin, "+
			"%d raíces. No se revisaron archivos por -solo-lock.\n", len(bloqueo.Artefactos),
			conHuella(bloqueo), len(bloqueo.Artefactos)-conHuella(bloqueo), len(bloqueo.Raices))
		return nil, nil
	}

	raices, fallos := resolverRaices(bloqueo, sobreescritas, filepath.Dir(rutaLock))

	verificados := 0
	porPin := 0
	usadas := make(map[string]bool, len(raices))
	for _, artefacto := range bloqueo.Artefactos {
		if artefacto.Sha256 == "" {
			fmt.Fprintf(salida, "%s: fijado por pin (%s), sin archivo que comparar.\n",
				artefacto.Nombre, artefacto.Pin)
			porPin++
			continue
		}
		raiz, resuelta := raices[artefacto.Raiz]
		if !resuelta {
			fallos = append(fallos, fmt.Sprintf("%s: no se puede resolver la raíz %q, revisa raices del lockfile y -raiz",
				artefacto.Nombre, artefacto.Raiz))
			continue
		}
		fallos = append(fallos, verificarArchivo(raiz, artefacto)...)
		usadas[raiz] = true
		verificados++
	}
	if len(fallos) > 0 {
		sort.Strings(fallos)
		return fallos, nil
	}

	fmt.Fprintf(salida, "Verificados %d artefactos contra %s y %d por pin.\n", verificados,
		strings.Join(nombresOrdenados(usadas), ", "), porPin)
	return nil, nil
}

// leerBloqueo abre el lockfile y rechaza campos que el formato no conoce, para que una errata
// en una clave no pase inadvertida.
func leerBloqueo(ruta string) (Bloqueo, error) {
	crudo, err := os.ReadFile(ruta)
	if err != nil {
		return Bloqueo{}, fmt.Errorf("leer el lockfile: %w", err)
	}
	decodificador := json.NewDecoder(strings.NewReader(string(crudo)))
	decodificador.DisallowUnknownFields()
	var bloqueo Bloqueo
	if err := decodificador.Decode(&bloqueo); err != nil {
		return Bloqueo{}, fmt.Errorf("parsear el lockfile: %w", err)
	}
	return bloqueo, nil
}

// validarEstructura revisa el lockfile completo sin abrir ningún peso.
func validarEstructura(bloqueo Bloqueo) []string {
	var fallos []string
	if bloqueo.Version != VersionDelLock {
		fallos = append(fallos, fmt.Sprintf("version: se esperaba %d y el lockfile trae %d",
			VersionDelLock, bloqueo.Version))
	}
	if len(bloqueo.Raices) == 0 {
		fallos = append(fallos, "raices: el lockfile no declara ninguna raíz de modelos")
	}
	for _, nombre := range nombresOrdenadosDe(bloqueo.Raices) {
		donde := "raices[" + nombre + "]"
		if strings.TrimSpace(nombre) == "" {
			fallos = append(fallos, "raices: hay una raíz sin nombre")
			continue
		}
		if fallo := validarRaiz(bloqueo.Raices[nombre]); fallo != "" {
			fallos = append(fallos, donde+": "+fallo)
		}
	}
	if len(bloqueo.Artefactos) == 0 {
		return append(fallos, "artefactos: el lockfile no declara ninguno")
	}

	vistos := make(map[string]bool, len(bloqueo.Artefactos))
	for indice, artefacto := range bloqueo.Artefactos {
		donde := fmt.Sprintf("artefactos[%d]", indice)
		if strings.TrimSpace(artefacto.Nombre) == "" {
			fallos = append(fallos, donde+": falta nombre")
		} else {
			donde += " (" + artefacto.Nombre + ")"
			if vistos[artefacto.Nombre] {
				fallos = append(fallos, donde+": nombre repetido")
			}
			vistos[artefacto.Nombre] = true
		}

		obligatorios := []struct {
			campo string
			valor string
		}{
			{"origen", artefacto.Origen},
			{"ruta", artefacto.Ruta},
			{"licencia", artefacto.Licencia},
			{"version_herramienta", artefacto.VersionHerramienta},
		}
		for _, obligatorio := range obligatorios {
			if strings.TrimSpace(obligatorio.valor) == "" {
				fallos = append(fallos, donde+": falta "+obligatorio.campo)
			}
		}

		if strings.TrimSpace(artefacto.Ruta) != "" {
			if fallo := validarRuta(artefacto.Ruta); fallo != "" {
				fallos = append(fallos, donde+": "+fallo)
			}
		}

		if _, err := time.Parse(FormatoFecha, artefacto.FechaCaptura); err != nil {
			fallos = append(fallos, fmt.Sprintf("%s: fecha_captura %q no está en formato %s",
				donde, artefacto.FechaCaptura, FormatoFecha))
		}

		switch {
		case artefacto.Sha256 != "" && artefacto.Pin != "":
			fallos = append(fallos, donde+": declara sha256 y pin a la vez, elige uno")
		case artefacto.Sha256 == "" && artefacto.Pin == "":
			fallos = append(fallos, donde+": falta sha256 o pin")
		case artefacto.Sha256 != "":
			if !huellaValida(artefacto.Sha256) {
				fallos = append(fallos, fmt.Sprintf("%s: sha256 %q no es hexadecimal en minúsculas de 64 caracteres",
					donde, artefacto.Sha256))
			}
			if artefacto.Bytes <= 0 {
				fallos = append(fallos, donde+": bytes debe ser positivo cuando hay sha256")
			}
		}

		if artefacto.Sha256 != "" || strings.TrimSpace(artefacto.Raiz) != "" {
			fallos = append(fallos, validarRaizDelArtefacto(bloqueo, donde, artefacto)...)
		}
	}
	return fallos
}

// validarRaizDelArtefacto exige que el artefacto con huella nombre una raíz declarada.
func validarRaizDelArtefacto(bloqueo Bloqueo, donde string, artefacto Artefacto) []string {
	nombre := strings.TrimSpace(artefacto.Raiz)
	if nombre == "" {
		return []string{donde + ": falta raiz"}
	}
	if strings.ContainsAny(nombre, `/\`) {
		return []string{donde + ": raiz debe ser el nombre de una raíz de raices, no una ruta"}
	}
	if _, hay := bloqueo.Raices[nombre]; !hay {
		return []string{donde + ": la raíz " + nombre + " no está declarada en raices"}
	}
	return nil
}

// validarRaiz acepta rutas relativas al lockfile y rutas absolutas, porque los pesos del
// contenedor se resuelven contra /opt/voz/modelos.
func validarRaiz(ruta string) string {
	if strings.TrimSpace(ruta) == "" {
		return "falta la ruta de la raíz"
	}
	limpia := filepath.ToSlash(filepath.Clean(filepath.FromSlash(ruta)))
	if limpia != ruta {
		return fmt.Sprintf("ruta %q no está normalizada, se esperaba %q", ruta, limpia)
	}
	return ""
}

// validarRuta exige una ruta relativa, limpia y sin escapes hacia arriba.
func validarRuta(ruta string) string {
	if filepath.IsAbs(ruta) {
		return "ruta no puede ser absoluta"
	}
	limpia := filepath.ToSlash(filepath.Clean(filepath.FromSlash(ruta)))
	if limpia != ruta {
		return fmt.Sprintf("ruta %q no está normalizada, se esperaba %q", ruta, limpia)
	}
	if limpia == ".." || strings.HasPrefix(limpia, "../") {
		return "ruta no puede salir de la raíz de modelos"
	}
	return ""
}

// huellaValida comprueba que el sha256 sea hexadecimal en minúsculas y de 64 caracteres.
func huellaValida(huella string) bool {
	if len(huella) != sha256.Size*2 || huella != strings.ToLower(huella) {
		return false
	}
	_, err := hex.DecodeString(huella)
	return err == nil
}

// resolverRaices traduce cada nombre de raíz a una ruta absoluta. Devuelve las que pudo resolver
// y un fallo por cada una que no, para que el reporte los junte todos.
func resolverRaices(bloqueo Bloqueo, sobreescritas map[string]string, carpetaDelLock string) (map[string]string, []string) {
	var fallos []string
	resueltas := make(map[string]string, len(bloqueo.Raices))

	for _, nombre := range nombresOrdenadosDe(bloqueo.Raices) {
		ruta := bloqueo.Raices[nombre]
		if sobreescrita, hay := sobreescritas[nombre]; hay {
			ruta = sobreescrita
		} else if general, hay := sobreescritas[""]; hay {
			ruta = general
		}
		absoluta, err := resolverRuta(ruta, carpetaDelLock)
		if err != nil {
			fallos = append(fallos, fmt.Sprintf("raices[%s]: %v", nombre, err))
			continue
		}
		resueltas[nombre] = absoluta
	}

	for _, nombre := range nombresOrdenadosDe(sobreescritas) {
		if nombre == "" {
			continue
		}
		if _, hay := bloqueo.Raices[nombre]; !hay {
			fallos = append(fallos, fmt.Sprintf("-raiz %s: el lockfile no declara esa raíz", nombre))
		}
	}
	sort.Strings(fallos)
	return resueltas, fallos
}

// resolverRuta convierte una raíz del lockfile en absoluta. Las relativas cuelgan de la carpeta
// que contiene el lockfile.
func resolverRuta(ruta, carpetaDelLock string) (string, error) {
	elegida := strings.TrimSpace(ruta)
	if elegida == "" {
		return "", errors.New("la raíz no tiene ruta")
	}
	if !filepath.IsAbs(elegida) {
		elegida = filepath.Join(carpetaDelLock, filepath.FromSlash(elegida))
	}
	absoluta, err := filepath.Abs(elegida)
	if err != nil {
		return "", fmt.Errorf("resolver la raíz de modelos: %w", err)
	}
	return absoluta, nil
}

// verificarArchivo compara existencia, tamaño y huella de un peso declarado.
func verificarArchivo(raiz string, artefacto Artefacto) []string {
	ruta := filepath.Join(raiz, filepath.FromSlash(artefacto.Ruta))
	info, err := os.Stat(ruta)
	if err != nil {
		return []string{fmt.Sprintf("%s: no se puede leer %s: %v", artefacto.Nombre, ruta, err)}
	}
	if info.IsDir() {
		return []string{fmt.Sprintf("%s: %s es un directorio y el lockfile declara un archivo",
			artefacto.Nombre, ruta)}
	}

	var fallos []string
	if info.Size() != artefacto.Bytes {
		fallos = append(fallos, fmt.Sprintf("%s: %s pesa %d bytes y el lockfile declara %d",
			artefacto.Nombre, ruta, info.Size(), artefacto.Bytes))
	}

	calculada, err := huellaDelArchivo(ruta)
	if err != nil {
		return append(fallos, fmt.Sprintf("%s: no se puede calcular la huella de %s: %v",
			artefacto.Nombre, ruta, err))
	}
	if calculada != artefacto.Sha256 {
		fallos = append(fallos, fmt.Sprintf("%s: la huella de %s no coincide, calculada %s y lockfile %s",
			artefacto.Nombre, ruta, calculada, artefacto.Sha256))
	}
	return fallos
}

// huellaDelArchivo calcula el sha256 leyendo por bloques, porque los pesos pasan de varios GB.
func huellaDelArchivo(ruta string) (string, error) {
	archivo, err := os.Open(ruta)
	if err != nil {
		return "", err
	}
	defer archivo.Close()

	huella := sha256.New()
	if _, err := io.Copy(huella, archivo); err != nil {
		return "", err
	}
	return hex.EncodeToString(huella.Sum(nil)), nil
}

// conHuella cuenta los artefactos verificables por bytes.
func conHuella(bloqueo Bloqueo) int {
	total := 0
	for _, artefacto := range bloqueo.Artefactos {
		if artefacto.Sha256 != "" {
			total++
		}
	}
	return total
}

// nombresOrdenadosDe devuelve las claves de un mapa de rutas ordenadas, para que el reporte no
// cambie entre corridas.
func nombresOrdenadosDe(valores map[string]string) []string {
	nombres := make([]string, 0, len(valores))
	for nombre := range valores {
		nombres = append(nombres, nombre)
	}
	sort.Strings(nombres)
	return nombres
}

// nombresOrdenados devuelve las claves de un conjunto ordenadas.
func nombresOrdenados(valores map[string]bool) []string {
	nombres := make([]string, 0, len(valores))
	for nombre := range valores {
		nombres = append(nombres, nombre)
	}
	sort.Strings(nombres)
	return nombres
}
