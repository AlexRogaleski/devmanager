// Package project descobre e descreve projetos no disco.
//
// Ele é o único lugar do Dev Manager que sabe interpretar arquivos de projeto
// (composer.json, composer.lock, .env). O resto do sistema consome só o
// struct Project, sem saber de onde a informação veio — assim, quando
// entrarem projetos Node ou Symfony, nada fora deste pacote muda.
package project

import "path/filepath"

// Kind identifica o tipo de projeto detectado.
//
// É um "tipo nomeado" sobre string: o valor continua sendo texto, mas o
// compilador passa a tratar Kind e string como tipos diferentes. Isso impede
// que alguém passe um nome de pasta onde se espera um tipo de projeto.
type Kind string

const (
	KindUnknown Kind = "desconhecido"
	KindPHP     Kind = "php"
	KindLaravel Kind = "laravel"
)

// Project é tudo que o Dev Manager sabe sobre um projeto no disco.
//
// Este struct é puro dado: sem conexões, sem estado de execução. Ele é o
// resultado de uma inspeção e pode ser serializado, cacheado ou mandado pela
// API do daemon sem surpresa.
type Project struct {
	// Name vem do nome da pasta, não do composer.json. O composer.json traria
	// "laravel/react-starter-kit" para vários projetos diferentes; a pasta é
	// única na máquina do dev e é o que vira domínio local depois.
	Name string `json:"name"`
	Path string `json:"path"` // sempre absoluto
	Kind Kind   `json:"kind"`

	// PHPConstraint é a exigência declarada em require.php, ex.: "^8.3".
	// Guardamos o texto cru: interpretar a faixa de versões é responsabilidade
	// do gerenciador de runtimes, não do detector.
	PHPConstraint string `json:"php_constraint,omitempty"`

	// LaravelRequire é o que o composer.json pede ("^13.17").
	// LaravelLocked é o que o composer.lock realmente instalou ("v13.17.1").
	// Os dois existem porque só o segundo diz o que está rodando de fato.
	LaravelRequire string `json:"laravel_require,omitempty"`
	LaravelLocked  string `json:"laravel_locked,omitempty"`

	// Sinais de prontidão: um projeto recém-clonado tem artisan, mas não tem
	// vendor nem .env. É essa diferença que o Dev Manager vai resolver sozinho.
	HasArtisan bool `json:"has_artisan"`
	HasVendor  bool `json:"has_vendor"`
	HasEnv     bool `json:"has_env"`
}

// IsLaravel informa se o projeto é Laravel.
//
// Métodos com receiver de ponteiro (p *Project) evitam copiar o struct a cada
// chamada e permitem que o método altere o valor. Por consistência, quando um
// tipo tem qualquer método de ponteiro, todos costumam ser de ponteiro.
func (p *Project) IsLaravel() bool {
	return p.Kind == KindLaravel
}

// Domain devolve o domínio local que este projeto terá quando o proxy existir.
func (p *Project) Domain() string {
	return p.Name + ".test"
}

// Ready informa se o projeto pode ser executado agora, sem preparo.
func (p *Project) Ready() bool {
	return p.HasVendor && p.HasEnv
}

// Missing lista o que falta para o projeto rodar, em ordem de execução.
//
// Devolver uma lista em vez de um booleano permite que a CLI hoje apenas
// imprima os passos, e que amanhã o comando `devm up` os execute.
func (p *Project) Missing() []string {
	// var sem valor inicial cria um slice nil. Em Go isso é seguro: append
	// funciona em slice nil, e len() devolve 0. Não precisa inicializar.
	var falta []string

	if !p.HasVendor {
		falta = append(falta, "composer install")
	}
	if !p.HasEnv {
		falta = append(falta, "cp .env.example .env")
	}
	if p.IsLaravel() && !p.HasEnv {
		falta = append(falta, "php artisan key:generate")
	}
	return falta
}

// ArtisanPath devolve o caminho absoluto do artisan deste projeto.
func (p *Project) ArtisanPath() string {
	return filepath.Join(p.Path, "artisan")
}
