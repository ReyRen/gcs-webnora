// migrate-harness-models is an operator-only, transactional import of legacy
// Harness model definitions into their verified owners' personal workspaces.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type entry struct {
	UserID     string                `json:"userID"`
	Provider   string                `json:"provider"`
	Route      string                `json:"route"`
	Model      string                `json:"model"`
	Name       string                `json:"name"`
	Parameters types.ModelParameters `json:"parameters"`
}

type selection struct {
	UserID   string `json:"userID"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	TenantID uint64 `json:"tenantID"`
	ID       string `json:"id"`
	Action   string `json:"action"`
}

func main() {
	input := flag.String("input", "", "0600 JSON array of explicitly reviewed legacy model entries")
	output := flag.String("output", "", "0600 result file containing model IDs, never credentials")
	apply := flag.Bool("apply", false, "commit the transaction; default is a read-only plan")
	flag.Parse()
	if err := run(*input, *output, *apply); err != nil {
		fmt.Fprintln(os.Stderr, "Harness model migration stopped:", err)
		os.Exit(1)
	}
}

func stableID(e entry) string {
	sum := sha256.Sum256([]byte(e.UserID + "\x00" + e.Route + "\x00" + e.Model))
	return "harness-" + hex.EncodeToString(sum[:16])
}

// Reuse only fully equal connection and capability settings, including the
// decrypted credential. Equal display names alone never merge model records.
func reusable(row types.Model, e entry) bool {
	return row.Name == e.Model && row.Type == types.ModelTypeKnowledgeQA && row.Source == types.ModelSourceRemote &&
		row.Status == types.ModelStatusActive && reflect.DeepEqual(row.Parameters, e.Parameters)
}

func run(input, output string, apply bool) error {
	if input == "" || output == "" || os.Getenv("HARNESS_MIGRATION_DSN") == "" {
		return errors.New("provide -input, -output and HARNESS_MIGRATION_DSN")
	}
	info, err := os.Stat(input)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16<<20 {
		return errors.New("input must be a private regular file of at most 16 MiB")
	}
	raw, err := os.ReadFile(input)
	if err != nil {
		return errors.New("cannot read the migration input")
	}
	var entries []entry
	if err := json.Unmarshal(raw, &entries); err != nil || len(entries) == 0 {
		return errors.New("input must be a non-empty model array")
	}
	db, err := gorm.Open(postgres.Open(os.Getenv("HARNESS_MIGRATION_DSN")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return errors.New("cannot connect to the migration database")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return errors.New("cannot open the migration database")
	}
	defer sqlDB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// Use the same DB > environment resolution and deployment extras as the
	// running backend. Importing must not weaken its outbound URL policy.
	var policy types.SystemSetting
	policyErr := db.WithContext(ctx).Where("key = ?", "ssrf.whitelist").First(&policy).Error
	if policyErr == nil {
		var hosts []string
		if json.Unmarshal(policy.Value, &hosts) != nil {
			return errors.New("cannot resolve the backend SSRF policy")
		}
		utils.SetSSRFWhitelistFromRaw(strings.Join(hosts, ",") + "," + os.Getenv("SSRF_WHITELIST_EXTRA"))
	} else if !errors.Is(policyErr, gorm.ErrRecordNotFound) {
		return errors.New("cannot inspect the backend SSRF policy")
	}
	for index, e := range entries {
		if e.UserID == "" || e.Provider == "" || e.Route == "" || e.Model == "" ||
			strings.ContainsAny(e.UserID+e.Route+e.Model, "\x00\r\n") {
			return errors.New("each entry requires an explicit platform identity, route and model")
		}
		if err := utils.ValidateURLForSSRF(e.Parameters.BaseURL); err != nil {
			return errors.New("a legacy endpoint is rejected by the current SSRF policy")
		}
		params := e.Parameters
		if err := modelruntime.ValidateRow(e.Model, types.ModelTypeKnowledgeQA, &params); err != nil {
			return errors.New("a legacy model is not supported by the current model catalog")
		}
		entries[index].Parameters = params
		if e.Parameters.APIKey != "" && utils.GetAESKey() == nil {
			return errors.New("SYSTEM_AES_KEY must be configured before importing credentials")
		}
	}
	var result []selection
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if apply && tx.Exec("SELECT pg_advisory_xact_lock(20261111, 1)").Error != nil {
			return errors.New("cannot acquire the import transaction lock")
		}
		for _, e := range entries {
			var scopes []uint64
			// The persisted SSO identity, home workspace, immutable JFS owner and
			// active owner membership must all agree. No input-supplied tenant ID.
			err := tx.Raw(`SELECT u.tenant_id FROM platform_sso_identities i
JOIN users u ON u.id = i.user_id AND u.deleted_at IS NULL AND u.is_active = true
JOIN platform_storage_bindings b ON b.tenant_id = u.tenant_id AND b.provider = i.provider AND b.subject = i.subject
JOIN tenant_members m ON m.user_id = u.id AND m.tenant_id = u.tenant_id AND m.role = 'owner' AND m.status = 'active'
WHERE i.provider = ? AND i.subject = ?`, e.Provider, e.UserID).Scan(&scopes).Error
			if err != nil || len(scopes) != 1 || scopes[0] == 0 {
				return errors.New("a model owner has no uniquely verified personal workspace")
			}
			tenant := scopes[0]
			var rows []types.Model
			if tx.Where("tenant_id = ? AND type = ? AND is_builtin = false", tenant, types.ModelTypeKnowledgeQA).Find(&rows).Error != nil {
				return errors.New("cannot inspect the owner's existing models")
			}
			id, action := stableID(e), "create"
			for _, row := range rows {
				if reusable(row, e) {
					id, action = row.ID, "reuse"
					break
				}
				if row.ID == id {
					return errors.New("an earlier import has different settings; no model was overwritten")
				}
			}
			if apply && action == "create" {
				model := types.Model{ID: id, TenantID: tenant, Name: e.Model, DisplayName: e.Name,
					Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote, Status: types.ModelStatusActive,
					Description: "Imported from the owner's Harness provider " + e.Route, Parameters: e.Parameters}
				if tx.Create(&model).Error != nil {
					return errors.New("cannot insert a model; the entire import transaction was rolled back")
				}
			}
			result = append(result, selection{UserID: e.UserID, Provider: e.Route, Model: e.Model, TenantID: tenant, ID: id, Action: action})
		}
		return nil
	})
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(struct {
		Applied bool        `json:"applied"`
		Models  []selection `json:"models"`
	}{apply, result}, "", "  ")
	if err != nil {
		return errors.New("cannot encode the import report")
	}
	if err := os.WriteFile(output, append(encoded, '\n'), 0600); err != nil {
		return errors.New("database import completed but the report could not be saved; reconcile IDs before retrying")
	}
	fmt.Printf("Harness model migration: apply=%t, models=%d; private report saved\n", apply, len(result))
	return nil
}
