package database

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/javinizer/javinizer-go/internal/logging"
	"github.com/javinizer/javinizer-go/internal/models"
)

// ActressAliasRepository persists and queries actress alias records that map
// alternate names to their canonical actress name.
type ActressAliasRepository struct {
	*BaseRepository[models.ActressAlias, uint]
}

// ErrActressAliasAmbiguous means one normalized alias key points at different
// canonical owners. Legacy databases may contain these rows because alias_name
// was historically unique only in its raw form; callers must not choose one.
var ErrActressAliasAmbiguous = errors.New("normalized actress alias has conflicting canonical owners")

func validateNormalizedAliasRows(aliases []models.ActressAlias) error {
	owners := make(map[string]string, len(aliases))
	for i := range aliases {
		aliasKey := models.NormalizeActressNameKey(aliases[i].AliasName)
		ownerKey := models.NormalizeActressNameKey(aliases[i].CanonicalName)
		if existing, ok := owners[aliasKey]; ok && existing != ownerKey {
			return fmt.Errorf("%w: %q", ErrActressAliasAmbiguous, aliases[i].AliasName)
		}
		owners[aliasKey] = ownerKey
	}
	return nil
}

func normalizedActressAliasesTx(tx *gorm.DB, aliasName string) ([]models.ActressAlias, error) {
	key := models.NormalizeActressNameKey(aliasName)
	if key == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var aliases []models.ActressAlias
	if err := tx.Where("alias_name_key = ?", key).Order("id").Find(&aliases).Error; err != nil {
		return nil, err
	}
	if len(aliases) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	if err := validateNormalizedAliasRows(aliases); err != nil {
		return nil, err
	}
	return aliases, nil
}

func normalizedAliasesForCanonicalTx(tx *gorm.DB, canonicalName string) ([]models.ActressAlias, error) {
	targetKey := models.NormalizeActressNameKey(canonicalName)
	if targetKey == "" {
		return nil, nil
	}
	var aliases []models.ActressAlias
	if err := tx.Where("canonical_name_key = ?", targetKey).Order("id").Find(&aliases).Error; err != nil {
		return nil, err
	}
	for i := range aliases {
		if models.NormalizeActressNameKey(aliases[i].AliasName) == "" {
			continue
		}
		if _, err := normalizedActressAliasesTx(tx, aliases[i].AliasName); err != nil {
			return nil, err
		}
	}
	return aliases, nil
}

func retargetNormalizedCanonicalAliasesTx(tx *gorm.DB, oldCanonicalName, newCanonicalName string) error {
	aliases, err := normalizedAliasesForCanonicalTx(tx, oldCanonicalName)
	if err != nil {
		return err
	}
	if len(aliases) == 0 {
		return nil
	}
	ids := make([]uint, len(aliases))
	for i := range aliases {
		ids[i] = aliases[i].ID
	}
	return tx.Model(&models.ActressAlias{}).Where("id IN ?", ids).Updates(map[string]interface{}{
		colCanonicalName:     newCanonicalName,
		"canonical_name_key": models.NormalizeActressNameKey(newCanonicalName),
		colUpdatedAt:         time.Now().UTC(),
	}).Error
}

func updateNormalizedActressAliasesTx(tx *gorm.DB, alias *models.ActressAlias) error {
	existing, err := normalizedActressAliasesTx(tx, alias.AliasName)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err := tx.Create(alias).Error; err != nil {
			return wrapDBErr("create", fmt.Sprintf("actress alias %s", alias.AliasName), err)
		}
		return nil
	}
	if err != nil {
		return wrapDBErr("find", fmt.Sprintf("actress alias %s", alias.AliasName), err)
	}
	alias.ID = existing[0].ID
	alias.CreatedAt = existing[0].CreatedAt
	ids := make([]uint, len(existing))
	for i := range existing {
		ids[i] = existing[i].ID
	}
	if err := tx.Model(&models.ActressAlias{}).Where("id IN ?", ids).Updates(map[string]interface{}{
		colCanonicalName:     alias.CanonicalName,
		"canonical_name_key": models.NormalizeActressNameKey(alias.CanonicalName),
		colUpdatedAt:         time.Now().UTC(),
	}).Error; err != nil {
		return wrapDBErr("update", fmt.Sprintf("actress alias %s", alias.AliasName), err)
	}
	return nil
}

func backfillActressAliasNameKeys(ctx context.Context, db *gorm.DB) error {
	var aliases []models.ActressAlias
	if err := db.WithContext(ctx).Where("alias_name_key = ? OR canonical_name_key = ?", "", "").Find(&aliases).Error; err != nil {
		return wrapDBErr("list", "actress aliases missing normalized keys", err)
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := range aliases {
			updates := map[string]interface{}{
				"alias_name_key":     models.NormalizeActressNameKey(aliases[i].AliasName),
				"canonical_name_key": models.NormalizeActressNameKey(aliases[i].CanonicalName),
			}
			if err := tx.Model(&models.ActressAlias{}).Where("id = ?", aliases[i].ID).Updates(updates).Error; err != nil {
				return wrapDBErr("backfill", fmt.Sprintf("actress alias %d normalized keys", aliases[i].ID), err)
			}
		}
		return nil
	})
}

// NewActressAliasRepository constructs an ActressAliasRepository backed by
// the given DB.
func NewActressAliasRepository(db *DB) *ActressAliasRepository {
	return &ActressAliasRepository{
		BaseRepository: NewBaseRepository[models.ActressAlias, uint](
			db, "actress alias",
			func(a models.ActressAlias) string { return a.AliasName },
			WithNewEntity[models.ActressAlias, uint](func() models.ActressAlias { return models.ActressAlias{} }),
		),
	}
}

// Create inserts a new actress alias record without stealing a normalized key
// already owned by another canonical identity.
func (r *ActressAliasRepository) Create(ctx context.Context, alias *models.ActressAlias) error {
	return r.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existing, err := normalizedActressAliasesTx(tx, alias.AliasName)
		if err == nil {
			if models.NormalizeActressNameKey(existing[0].CanonicalName) != models.NormalizeActressNameKey(alias.CanonicalName) {
				return fmt.Errorf("create actress alias %s: %w", alias.AliasName, ErrActressAliasAmbiguous)
			}
			alias.ID = existing[0].ID
			alias.CreatedAt = existing[0].CreatedAt
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return wrapDBErr("find", fmt.Sprintf("actress alias %s", alias.AliasName), err)
		}
		if err := tx.Create(alias).Error; err != nil {
			return wrapDBErr("create", fmt.Sprintf("actress alias %s", alias.AliasName), err)
		}
		return nil
	})
}

// Upsert inserts the alias when new or updates the existing alias record
// keyed by alias name.
func (r *ActressAliasRepository) Upsert(ctx context.Context, alias *models.ActressAlias) error {
	return r.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return updateNormalizedActressAliasesTx(tx, alias)
	})
}

// UpsertTx upserts an alias within the given transaction.
func (r *ActressAliasRepository) UpsertTx(tx *gorm.DB, alias *models.ActressAlias) error {
	return updateNormalizedActressAliasesTx(tx, alias)
}

// FindByAliasName loads the alias record with the given normalized alias name.
func (r *ActressAliasRepository) FindByAliasName(ctx context.Context, aliasName string) (*models.ActressAlias, error) {
	aliases, err := normalizedActressAliasesTx(r.GetDB().WithContext(ctx), aliasName)
	if err != nil {
		return nil, wrapDBErr("find", fmt.Sprintf("actress alias %s", aliasName), err)
	}
	return &aliases[0], nil
}

// FindByCanonicalName returns every alias whose canonical owner normalizes to
// the given name. Conflicting normalized alias owners fail closed.
func (r *ActressAliasRepository) FindByCanonicalName(ctx context.Context, canonicalName string) ([]models.ActressAlias, error) {
	aliases, err := normalizedAliasesForCanonicalTx(r.GetDB().WithContext(ctx), canonicalName)
	if err != nil {
		return nil, wrapDBErr("find", fmt.Sprintf("actress aliases for %s", canonicalName), err)
	}
	sort.SliceStable(aliases, func(i, j int) bool { return aliases[i].AliasName < aliases[j].AliasName })
	return aliases, nil
}

// List returns all actress alias records.
func (r *ActressAliasRepository) List(ctx context.Context) ([]models.ActressAlias, error) {
	return r.ListAll(ctx)
}

// Delete removes the alias record with the given alias name.
func (r *ActressAliasRepository) Delete(ctx context.Context, aliasName string) error {
	return r.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		aliases, err := normalizedActressAliasesTx(tx, aliasName)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return wrapDBErr("find", fmt.Sprintf("actress alias %s", aliasName), err)
		}
		ids := make([]uint, len(aliases))
		for i := range aliases {
			ids[i] = aliases[i].ID
		}
		if err := tx.Delete(&models.ActressAlias{}, ids).Error; err != nil {
			return wrapDBErr("delete", fmt.Sprintf("actress alias %s", aliasName), err)
		}
		return nil
	})
}

// GetAliasMap returns one deterministic mapping per normalized alias key.
func (r *ActressAliasRepository) GetAliasMap(ctx context.Context) (map[string]string, error) {
	var aliases []models.ActressAlias
	if err := r.GetDB().WithContext(ctx).Order("id").Find(&aliases).Error; err != nil {
		return nil, wrapDBErr("list", "actress aliases", err)
	}
	if err := validateNormalizedAliasRows(aliases); err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, a := range aliases {
		key := models.NormalizeActressNameKey(a.AliasName)
		if key == "" {
			continue
		}
		if _, exists := result[key]; !exists {
			result[key] = a.CanonicalName
		}
	}
	return result, nil
}

// AliasGroup is the set of all known names for a single performer: the
// canonical name plus every alias that resolves to it.
type AliasGroup struct {
	Canonical string   // The canonical (preferred) name; empty when name is unknown.
	Names     []string // Canonical first, then aliases, deduplicated, order-stable.
}

// GetAliasGroup resolves a name to its full known-names group. The input may
// be either an alias or a canonical name. When the name is not present in the
// alias table at all, Canonical is empty and Names is nil — callers should
// treat this as "no known aliases, nothing to choose between". The returned
// Names slice is deduplicated and order-stable (canonical first, then aliases
// in the order the database returns them).
func (r *ActressAliasRepository) GetAliasGroup(ctx context.Context, name string) (AliasGroup, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return AliasGroup{}, nil
	}

	// Resolve the canonical form. Prefer treating `name` as a canonical when
	// rows point at it; only follow the alias mapping when `name` is not itself
	// a canonical. This avoids returning the wrong group when a name is both a
	// former name (alias) of one performer and the current name (canonical) of
	// another. Order is deterministic (FindByCanonicalName sorts by alias_name)
	// so the dropdown is stable.
	canonical := name
	matching, err := r.FindByCanonicalName(ctx, name)
	if err != nil {
		return AliasGroup{}, err
	}
	if len(matching) > 0 {
		canonical = matching[0].CanonicalName
		lowestID := matching[0].ID
		for i := 1; i < len(matching); i++ {
			if matching[i].ID < lowestID {
				lowestID = matching[i].ID
				canonical = matching[i].CanonicalName
			}
		}
	}
	if len(matching) == 0 {
		// `name` is not a canonical — try following it as an alias. The alias
		// row itself guarantees FindByCanonicalName returns at least one row.
		if a, ferr := r.FindByAliasName(ctx, name); ferr == nil {
			canonical = a.CanonicalName
			matching, err = r.FindByCanonicalName(ctx, canonical)
			if err != nil {
				return AliasGroup{}, err
			}
		} else if !IsNotFound(ferr) {
			return AliasGroup{}, ferr
		}
	}
	// FindByCanonicalName uses a Find() query, which returns an empty slice
	// (not IsNotFound) when nothing matches. An empty result means the name is
	// neither an alias nor a canonical in the table — there is no group.
	if len(matching) == 0 {
		return AliasGroup{}, nil
	}

	seen := make(map[string]struct{}, len(matching)+1)
	names := make([]string, 0, len(matching)+1)
	add := func(n string) {
		if n == "" {
			return
		}
		if _, ok := seen[n]; ok {
			return
		}
		seen[n] = struct{}{}
		names = append(names, n)
	}

	add(canonical)
	for _, a := range matching {
		add(a.AliasName)
	}

	return AliasGroup{Canonical: canonical, Names: names}, nil
}

var defaultActressAliases []models.ActressAlias

func init() {
	// Curated rename mappings for well-known AV actresses who have changed
	// stage names. Canonical form is the most current name. Each alias maps
	// directly to the canonical (no transitive chains) so that single-hop
	// resolution collapses all of a performer's credits into one entry.
	//
	// Sources: ja.wikipedia.org and community wikis (seesaawiki av_neme,
	// av-wiki.net), cross-checked across multiple titles.
	defaultActressAliases = []models.ActressAlias{
		// 新セリナ — renamed 青木桃 → 朝日芹奈 (2022-10) → 堤セリナ (2024-03) → 新セリナ (2025-04)
		{AliasName: "青木桃", CanonicalName: "新セリナ"},
		{AliasName: "朝日芹奈", CanonicalName: "新セリナ"},
		{AliasName: "堤セリナ", CanonicalName: "新セリナ"},
		// 尾崎えりか — renamed 与田さくら → 尾崎えりか (2022-09)
		{AliasName: "与田さくら", CanonicalName: "尾崎えりか"},
		// 日向ゆら — renamed 広瀬みつき → 日向ゆら (2022-08)
		{AliasName: "広瀬みつき", CanonicalName: "日向ゆら"},
	}
}

// SeedDefaultActressAliases inserts the built-in default actress alias mappings
// into the repository. Existing user-curated aliases are preserved: only
// alias names that are not already present are inserted, so a user's choice of
// canonical name for an alias is never overwritten by the seed.
func SeedDefaultActressAliases(ctx context.Context, repo ActressAliasRepositoryInterface) {
	for i := range defaultActressAliases {
		a := defaultActressAliases[i]
		_, err := repo.FindByAliasName(ctx, a.AliasName)
		if err == nil {
			// Already present (possibly user-curated) — leave it untouched.
			continue
		}
		if !IsNotFound(err) {
			logging.Warnf("failed to seed actress alias %q: %v", a.AliasName, err)
			continue
		}
		if err := repo.Create(ctx, &a); err != nil {
			logging.Warnf("failed to seed actress alias %q: %v", a.AliasName, err)
		}
	}
}
