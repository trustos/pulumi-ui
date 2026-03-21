package db

import (
	"database/sql"
	"fmt"
	"time"
)

// CustomProgram represents a user-defined, database-stored Pulumi YAML program.
type CustomProgram struct {
	Name        string    `json:"name"`
	DisplayName string    `json:"displayName"`
	Description string    `json:"description"`
	ProgramYAML string    `json:"programYaml"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// CustomProgramStore handles persistence of user-defined programs.
type CustomProgramStore struct {
	db *sql.DB
}

func NewCustomProgramStore(db *sql.DB) *CustomProgramStore {
	return &CustomProgramStore{db: db}
}

func (s *CustomProgramStore) Create(p CustomProgram) error {
	_, err := s.db.Exec(`
		INSERT INTO custom_programs (name, display_name, description, program_yaml)
		VALUES (?, ?, ?, ?)`,
		p.Name, p.DisplayName, p.Description, p.ProgramYAML,
	)
	if err != nil {
		return fmt.Errorf("create custom program: %w", err)
	}
	return nil
}

func (s *CustomProgramStore) Get(name string) (CustomProgram, error) {
	var p CustomProgram
	var createdAt, updatedAt int64
	err := s.db.QueryRow(`
		SELECT name, display_name, description, program_yaml, created_at, updated_at
		FROM custom_programs WHERE name = ?`, name).
		Scan(&p.Name, &p.DisplayName, &p.Description, &p.ProgramYAML, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return p, fmt.Errorf("program %q not found", name)
	}
	if err != nil {
		return p, fmt.Errorf("get custom program: %w", err)
	}
	p.CreatedAt = time.Unix(createdAt, 0)
	p.UpdatedAt = time.Unix(updatedAt, 0)
	return p, nil
}

func (s *CustomProgramStore) List() ([]CustomProgram, error) {
	rows, err := s.db.Query(`
		SELECT name, display_name, description, program_yaml, created_at, updated_at
		FROM custom_programs ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list custom programs: %w", err)
	}
	defer rows.Close()

	var programs []CustomProgram
	for rows.Next() {
		var p CustomProgram
		var createdAt, updatedAt int64
		if err := rows.Scan(&p.Name, &p.DisplayName, &p.Description, &p.ProgramYAML, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		p.CreatedAt = time.Unix(createdAt, 0)
		p.UpdatedAt = time.Unix(updatedAt, 0)
		programs = append(programs, p)
	}
	return programs, rows.Err()
}

func (s *CustomProgramStore) Update(name string, displayName, description, programYAML string) error {
	res, err := s.db.Exec(`
		UPDATE custom_programs
		SET display_name = ?, description = ?, program_yaml = ?, updated_at = unixepoch()
		WHERE name = ?`,
		displayName, description, programYAML, name,
	)
	if err != nil {
		return fmt.Errorf("update custom program: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("program %q not found", name)
	}
	return nil
}

func (s *CustomProgramStore) Delete(name string) error {
	res, err := s.db.Exec(`DELETE FROM custom_programs WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("delete custom program: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("program %q not found", name)
	}
	return nil
}
