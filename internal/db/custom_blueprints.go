package db

import (
	"database/sql"
	"fmt"
	"time"
)

// CustomBlueprint represents a user-defined, database-stored Pulumi YAML blueprint.
type CustomBlueprint struct {
	Name        string    `json:"name"`
	DisplayName string    `json:"displayName"`
	Description string    `json:"description"`
	BlueprintYAML string    `json:"blueprintYaml"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// CustomBlueprintStore handles persistence of user-defined blueprints.
type CustomBlueprintStore struct {
	db *sql.DB
}

func NewCustomBlueprintStore(db *sql.DB) *CustomBlueprintStore {
	return &CustomBlueprintStore{db: db}
}

func (s *CustomBlueprintStore) Create(p CustomBlueprint) error {
	_, err := s.db.Exec(`
		INSERT INTO custom_blueprints (name, display_name, description, blueprint_yaml)
		VALUES (?, ?, ?, ?)`,
		p.Name, p.DisplayName, p.Description, p.BlueprintYAML,
	)
	if err != nil {
		return fmt.Errorf("create custom blueprint: %w", err)
	}
	return nil
}

func (s *CustomBlueprintStore) Get(name string) (CustomBlueprint, error) {
	var p CustomBlueprint
	var createdAt, updatedAt int64
	err := s.db.QueryRow(`
		SELECT name, display_name, description, blueprint_yaml, created_at, updated_at
		FROM custom_blueprints WHERE name = ?`, name).
		Scan(&p.Name, &p.DisplayName, &p.Description, &p.BlueprintYAML, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return p, fmt.Errorf("blueprint %q not found", name)
	}
	if err != nil {
		return p, fmt.Errorf("get custom blueprint: %w", err)
	}
	p.CreatedAt = time.Unix(createdAt, 0)
	p.UpdatedAt = time.Unix(updatedAt, 0)
	return p, nil
}

func (s *CustomBlueprintStore) List() ([]CustomBlueprint, error) {
	rows, err := s.db.Query(`
		SELECT name, display_name, description, blueprint_yaml, created_at, updated_at
		FROM custom_blueprints ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list custom blueprints: %w", err)
	}
	defer rows.Close()

	var result []CustomBlueprint
	for rows.Next() {
		var p CustomBlueprint
		var createdAt, updatedAt int64
		if err := rows.Scan(&p.Name, &p.DisplayName, &p.Description, &p.BlueprintYAML, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		p.CreatedAt = time.Unix(createdAt, 0)
		p.UpdatedAt = time.Unix(updatedAt, 0)
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *CustomBlueprintStore) Update(name string, displayName, description, blueprintYAML string) error {
	res, err := s.db.Exec(`
		UPDATE custom_blueprints
		SET display_name = ?, description = ?, blueprint_yaml = ?, updated_at = unixepoch()
		WHERE name = ?`,
		displayName, description, blueprintYAML, name,
	)
	if err != nil {
		return fmt.Errorf("update custom blueprint: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("blueprint %q not found", name)
	}
	return nil
}

func (s *CustomBlueprintStore) Delete(name string) error {
	res, err := s.db.Exec(`DELETE FROM custom_blueprints WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("delete custom blueprint: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("blueprint %q not found", name)
	}
	return nil
}
