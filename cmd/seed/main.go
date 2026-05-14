// Seed popula o banco com dados de teste para desenvolvimento.
// Uso: DATABASE_URL="..." go run ./cmd/seed
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"matchcamp/internal/auth"
	db "matchcamp/internal/database/db"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/pgtype"
)

var seedProfiles = []struct {
	email       string
	displayName string
	bio         string
	course      string
	campus      string
	birthDate   string
	photoURL    string
}{
	{"ana@uni.br", "Ana Silva", "Adoro filmes e cafe", "Ciencia da Computacao", "Campus Norte", "2001-03-15", "https://i.pravatar.cc/300?img=1"},
	{"bruna@uni.br", "Bruna Costa", "Apaixonada por musica e livros", "Engenharia Civil", "Campus Sul", "2000-07-22", "https://i.pravatar.cc/300?img=2"},
	{"carlos@uni.br", "Carlos Mendes", "Dev, gamer e fa de pizza", "Sistemas de Informacao", "Campus Norte", "1999-11-08", "https://i.pravatar.cc/300?img=3"},
	{"diana@uni.br", "Diana Rocha", "Viajante nas horas vagas", "Administracao", "Campus Leste", "2001-05-30", "https://i.pravatar.cc/300?img=4"},
	{"eduardo@uni.br", "Eduardo Lima", "Engenheiro de plantao", "Engenharia Mecanica", "Campus Sul", "2000-09-14", "https://i.pravatar.cc/300?img=5"},
	{"fernanda@uni.br", "Fernanda Nunes", "Amo dancar e cozinhar", "Psicologia", "Campus Norte", "2002-01-20", "https://i.pravatar.cc/300?img=6"},
	{"gabriel@uni.br", "Gabriel Souza", "Atleta e estudante curioso", "Educacao Fisica", "Campus Leste", "2000-06-05", "https://i.pravatar.cc/300?img=7"},
	{"helena@uni.br", "Helena Martins", "Arte e tecnologia se encontram", "Design", "Campus Norte", "2001-12-18", "https://i.pravatar.cc/300?img=8"},
	{"igor@uni.br", "Igor Fernandes", "Ciencia e cafe, nessa ordem", "Fisica", "Campus Sul", "1999-04-25", "https://i.pravatar.cc/300?img=9"},
	{"julia@uni.br", "Julia Pereira", "Direito e justica social", "Direito", "Campus Norte", "2001-08-11", "https://i.pravatar.cc/300?img=10"},
}

func main() {
	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dbURL == "" {
		dbURL = "postgres://matchcamp:matchcamp@localhost:5432/matchcamp?sslmode=disable"
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer pool.Close()

	q := db.New(pool)

	hash, err := auth.HashPassword("senha1234")
	if err != nil {
		log.Fatalf("hash password: %v", err)
	}

	created := 0
	for _, p := range seedProfiles {
		user, err := q.CreatePasswordUser(ctx, db.CreatePasswordUserParams{
			Email:        p.email,
			DisplayName:  p.displayName,
			PasswordHash: pgtype.Text{String: hash, Valid: true},
		})
		if err != nil {
			log.Printf("skip %s: %v", p.email, err)
			continue
		}

		var birthDate pgtype.Date
		if t, err := time.Parse("2006-01-02", p.birthDate); err == nil {
			birthDate = pgtype.Date{Time: t, Valid: true}
		}

		if err := q.UpsertProfile(ctx, db.UpsertProfileParams{
			UserID:    user.ID,
			Bio:       p.bio,
			Course:    p.course,
			Campus:    p.campus,
			BirthDate: birthDate,
		}); err != nil {
			log.Printf("profile %s: %v", p.email, err)
			continue
		}

		if _, err := q.UpsertProfilePhoto(ctx, db.UpsertProfilePhotoParams{
			UserID:   user.ID,
			Url:      p.photoURL,
			Position: 0,
		}); err != nil {
			log.Printf("photo %s: %v", p.email, err)
		}

		if _, err := q.UpdateProfileVisibility(ctx, db.UpdateProfileVisibilityParams{
			UserID:  user.ID,
			Visible: true,
		}); err != nil {
			log.Printf("visibility %s: %v", p.email, err)
		}

		fmt.Printf("ok  %s (%s)\n", p.displayName, p.email)
		created++
	}

	fmt.Printf("\n%d usuarios criados. Senha padrao: senha1234\n", created)
	fmt.Println("Para testar: go run ./cmd/api  e abra http://localhost:8080/docs")
}
