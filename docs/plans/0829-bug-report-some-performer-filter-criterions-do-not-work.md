# 0829 — [Bug Report] Some Performer Filter Criterions Do Not Work

**Status: SOLVED in `d9a09c804`.** This plan records what was done and
why, so the change can be re-implemented or reviewed without the original
context. It is a description of shipped work, not a proposal.

- Commit: `d9a09c804` — performer: implement the filter criteria the query builder was dropping (fixes #829)
- Area: `performer`
- Issue: https://github.com/stashapp/stash-box/issues/829

## What was wrong

From `docs/track/WORKLOG.md`:

> `PerformerQueryInput` declares ~24 filter fields and GraphQL accepts all of
> them. `buildPerformerQuery` handled 13. The other 11 were read from the input
> and **never used**:
>
> `eye_color` `hair_color` `height` `cup_size` `band_size` `waist_size`
> `hip_size` `breast_type` `career_start_year` `career_end_year` `tattoos`
> `piercings`
>
> The failure mode is a silent wrong answer, not an error — a caller setting
> `breast_type` got back every performer, in the same shape as a correct
> response. That is precisely what the reporter describes: "some work, some
> don't", with no error to explain the difference.
>
> Two helpers added:
>
> - `ApplyEnumCriterion` — generic over `~string`, so eye/hair/breast share one
>   implementation and cannot drift. `NOT_EQUALS` excludes NULLs: a performer
>   with no recorded eye color is *unknown*, not "some other colour".
> - `ApplyBodyModificationCriterion` — EXISTS semi-join over
>   `performer_tattoos` / `performer_piercings`.
>
> **A wrong assumption I had to correct mid-task:** I first wrote the body-mod
> helper against `performers.tattoos` as a jsonb array, because that is how the
> Stash *client* models it. The schema says
> `CREATE TABLE "performer_tattoos" (performer_id, location, description)` —
> relational, keyed `(performer_id, location)`. The test caught it as
> `syntax error at or near ")"`. EXISTS over the table is also strictly better:
> "location AND description" matching the *same row* then falls out for free, so

## Files touched

**implementation**

- `internal/service/performer/query.go`
- `internal/service/query/criterion.go`

**test**

- `internal/api/performer_filter_integration_test.go`
- `internal/api/performer_integration_test.go`

## The change

### `internal/service/performer/query.go`

```diff
diff --git a/internal/service/performer/query.go b/internal/service/performer/query.go
index c496f2b..9b3e6bf 100644
--- a/internal/service/performer/query.go
+++ b/internal/service/performer/query.go
@@ -179,6 +179,50 @@ func (s *Performer) buildPerformerQuery(psql sq.StatementBuilderType, input mode
+	if input.CupSize != nil {
+		query = queryhelper.ApplyStringCriterion(query, "performers.cup_size", input.CupSize)
+	}
+
+	// Enum criteria. Nullable columns, so IS NULL is a real query (#829): these
+	// were accepted by the schema and then silently dropped, so a query setting
+	// them returned every performer instead of the filtered set.
+	if input.EyeColor != nil {
+		query = queryhelper.ApplyEnumCriterion(query, "performers.eye_color", input.EyeColor.Value, input.EyeColor.Modifier)
+	}
+	if input.HairColor != nil {
+		query = queryhelper.ApplyEnumCriterion(query, "performers.hair_color", input.HairColor.Value, input.HairColor.Modifier)
+	}
+	if input.BreastType != nil {
+		query = queryhelper.ApplyEnumCriterion(query, "performers.breast_type", input.BreastType.Value, input.BreastType.Modifier)
+	}
+
+	// Int criteria
+	if input.Height != nil {
+		query = queryhelper.ApplyIntCriterion(query, "performers.height", input.Height)
+	}
+	if input.BandSize != nil {
+		query = queryhelper.ApplyIntCriterion(query, "performers.band_size", input.BandSize)
+	}
+	if input.WaistSize != nil {
+		query = queryhelper.ApplyIntCriterion(query, "performers.waist_size", input.WaistSize)
+	}
+	if input.HipSize != nil {
+		query = queryhelper.ApplyIntCriterion(query, "performers.hip_size", input.HipSize)
+	}
+	if input.CareerStartYear != nil {
+		query = queryhelper.ApplyIntCriterion(query, "performers.career_start_year", input.CareerStartYear)
+	}
+	if input.CareerEndYear != nil {
+		query = queryhelper.ApplyIntCriterion(query, "performers.career_end_year", input.CareerEndYear)
+	}
+
+	// Body modifications (tattoos, piercings) are relational tables.
+	if input.Tattoos != nil {
+		query = queryhelper.ApplyBodyModificationCriterion(query, "performer_tattoos", "performer_id", input.Tattoos)
+	}
+	if input.Piercings != nil {
+		query = queryhelper.ApplyBodyModificationCriterion(query, "performer_piercings", "performer_id", input.Piercings)
+	}
```

### `internal/service/query/criterion.go`

```diff
diff --git a/internal/service/query/criterion.go b/internal/service/query/criterion.go
index 567f883..eb09f2b 100644
--- a/internal/service/query/criterion.go
+++ b/internal/service/query/criterion.go
@@ -50,6 +50,115 @@ func ApplyMultiIDCriterion(query *sq.SelectBuilder, tableName, joinTable, fkColu
+// ApplyEnumCriterion applies a criterion whose value is a nullable enum column
+// (eye_color, hair_color, breast_type). Those are nullable varchar in the schema,
+// so IS NULL / NOT NULL are meaningful in their own right -- a performer with no
+// recorded eye color must be reachable by filtering for exactly that.
+//
+// Generic over the enum so eye/hair/breast share one implementation and cannot
+// drift apart.
+func ApplyEnumCriterion[T ~string](query sq.SelectBuilder, field string, value *T, modifier models.CriterionModifier) sq.SelectBuilder {
+	switch modifier {
+	case models.CriterionModifierEquals:
+		if value == nil {
+			return query.Where(field + " IS NULL")
+		}
+		return query.Where(sq.Eq{field: string(*value)})
+	case models.CriterionModifierNotEquals:
+		if value == nil {
+			return query.Where(field + " IS NOT NULL")
+		}
+		// A performer with no recorded value is not a non-match on "not this
+		// value" -- NULL means unknown, not "something else".
+		return query.Where(sq.And{
+			sq.NotEq{field: string(*value)},
+			sq.Expr(field + " IS NOT NULL"),
+		})
+	case models.CriterionModifierIsNull:
+		return query.Where(field + " IS NULL")
+	case models.CriterionModifierNotNull:
+		return query.Where(field + " IS NOT NULL")
+	default:
+		return query
+	}
+}
+
+// ApplyBodyModificationCriterion filters on a body modification (tattoo or
+// piercing) recorded for the performer.
+//
+// Tattoos and piercings are relational tables keyed (performer_id, location),
+// not jsonb columns on the performer, so this is an EXISTS semi-join. That is
+// also what makes "location AND description match the same row" fall out for
+// free: a performer with a left-shoulder tattoo and a separate wing tattoo does
+// not match a query asking for both, because no single row satisfies both
+// predicates.
+func ApplyBodyModificationCriterion(query sq.SelectBuilder, table, fkColumn string, criterion *models.BodyModificationCriterionInput) sq.SelectBuilder {
+	if criterion == nil {
+		return query
+	}
+
+	// A criterion with neither field set would match every performer that has
+	// any modification at all, which is almost never what the caller meant.
+	if criterion.Location == nil && criterion.Description == nil {
+		return query
+	}
+
+	mod := criterion.Modifier
+	negate := false
    ... (trimmed; run `git show` for the full diff)
```

## Tests

- `internal/api/performer_filter_integration_test.go`
- `internal/api/performer_integration_test.go`

These were mutation-checked: the fix was reverted, the test was run, and
it was required to fail. A test that survives that check is not evidence.

## Verify

```bash
go build ./... && go vet ./...
export POSTGRES_DB="$STASHBOX_TEST_DSN"   # test DSN, never commit it
go test -tags=integration -count=1 ./internal/api/
go test $(go list ./... | grep -vE 'internal/api$') -count=1
```

---
