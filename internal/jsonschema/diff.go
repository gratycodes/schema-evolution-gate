package jsonschema

import (
	"math"
	"slices"
	"strconv"
	"strings"
)

// Difference is one classified change between an original and an update
// schema. Type uses the same names as Confluent's JSON Schema diff so results
// can be compared one to one.
type Difference struct {
	Type string
	Path string // JSON pointer into the schemas, "#/properties/amount"
}

// Policy selects which classified changes count as compatible.
type Policy string

// Compatibility policies, matching Confluent's compatibilityPolicy setting.
const (
	Strict  Policy = "strict"
	Lenient Policy = "lenient"
)

var compatibleStrict = toSet(
	"ID_CHANGED", "DESCRIPTION_CHANGED", "TITLE_CHANGED", "DEFAULT_CHANGED", "SCHEMA_REMOVED", "TYPE_EXTENDED",
	"MAX_LENGTH_INCREASED", "MAX_LENGTH_REMOVED", "MIN_LENGTH_DECREASED", "MIN_LENGTH_REMOVED", "PATTERN_REMOVED",
	"MAXIMUM_INCREASED", "MAXIMUM_REMOVED", "MINIMUM_DECREASED", "MINIMUM_REMOVED",
	"EXCLUSIVE_MAXIMUM_INCREASED", "EXCLUSIVE_MAXIMUM_REMOVED", "EXCLUSIVE_MINIMUM_DECREASED", "EXCLUSIVE_MINIMUM_REMOVED",
	"MULTIPLE_OF_REDUCED", "MULTIPLE_OF_REMOVED",
	"REQUIRED_ATTRIBUTE_WITH_DEFAULT_ADDED", "REQUIRED_ATTRIBUTE_REMOVED",
	"DEPENDENCY_ARRAY_NARROWED", "DEPENDENCY_ARRAY_REMOVED", "DEPENDENCY_SCHEMA_REMOVED",
	"MAX_PROPERTIES_INCREASED", "MAX_PROPERTIES_REMOVED", "MIN_PROPERTIES_DECREASED", "MIN_PROPERTIES_REMOVED",
	"ADDITIONAL_PROPERTIES_ADDED", "ADDITIONAL_PROPERTIES_EXTENDED",
	"PROPERTY_WITH_EMPTY_SCHEMA_ADDED_TO_OPEN_CONTENT_MODEL", "REQUIRED_PROPERTY_WITH_DEFAULT_ADDED_TO_UNOPEN_CONTENT_MODEL",
	"OPTIONAL_PROPERTY_ADDED_TO_UNOPEN_CONTENT_MODEL", "PROPERTY_WITH_FALSE_REMOVED_FROM_CLOSED_CONTENT_MODEL",
	"PROPERTY_REMOVED_FROM_OPEN_CONTENT_MODEL", "PROPERTY_ADDED_IS_COVERED_BY_PARTIALLY_OPEN_CONTENT_MODEL",
	"PROPERTY_REMOVED_IS_COVERED_BY_PARTIALLY_OPEN_CONTENT_MODEL",
	"MAX_ITEMS_INCREASED", "MAX_ITEMS_REMOVED", "MIN_ITEMS_DECREASED", "MIN_ITEMS_REMOVED", "UNIQUE_ITEMS_REMOVED",
	"ADDITIONAL_ITEMS_ADDED", "ADDITIONAL_ITEMS_EXTENDED",
	"ITEM_WITH_EMPTY_SCHEMA_ADDED_TO_OPEN_CONTENT_MODEL", "ITEM_ADDED_TO_CLOSED_CONTENT_MODEL",
	"ITEM_WITH_FALSE_REMOVED_FROM_CLOSED_CONTENT_MODEL", "ITEM_REMOVED_FROM_OPEN_CONTENT_MODEL",
	"ITEM_ADDED_IS_COVERED_BY_PARTIALLY_OPEN_CONTENT_MODEL", "ITEM_REMOVED_IS_COVERED_BY_PARTIALLY_OPEN_CONTENT_MODEL",
	"ENUM_ARRAY_EXTENDED", "COMBINED_TYPE_EXTENDED", "PRODUCT_TYPE_NARROWED", "SUM_TYPE_EXTENDED", "NOT_TYPE_NARROWED",
)

var compatibleLenient = union(compatibleStrict, toSet(
	"ADDITIONAL_PROPERTIES_NARROWED", "ADDITIONAL_PROPERTIES_REMOVED",
	"REQUIRED_PROPERTY_WITH_DEFAULT_ADDED_TO_OPEN_CONTENT_MODEL", "OPTIONAL_PROPERTY_ADDED_TO_OPEN_CONTENT_MODEL",
	"PROPERTY_REMOVED_FROM_OPEN_CONTENT_MODEL", "PROPERTY_REMOVED_FROM_CLOSED_CONTENT_MODEL",
	"PROPERTY_ADDED_NOT_COVERED_BY_PARTIALLY_OPEN_CONTENT_MODEL", "PROPERTY_REMOVED_NOT_COVERED_BY_PARTIALLY_OPEN_CONTENT_MODEL",
))

func toSet(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, i := range items {
		m[i] = true
	}
	return m
}

func union(a, b map[string]bool) map[string]bool {
	out := make(map[string]bool, len(a)+len(b))
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

// IsCompatible reports whether a single difference type is allowed under p.
func IsCompatible(t string, p Policy) bool {
	if p == Lenient {
		return compatibleLenient[t]
	}
	return compatibleStrict[t]
}

// Compare classifies every change from original to update. For a BACKWARD
// check the original is the old schema (what the data was written with) and
// the update is the new one (what consumers will read with); for FORWARD the
// roles are swapped.
func Compare(original, update *Schema, p Policy) []Difference {
	c := &ctx{policy: p, path: "#/", inProgress: map[[2]*Schema]bool{}}
	c.compare(original, update)
	return c.diffs
}

// Incompatible filters a Compare result down to the breaking changes.
func Incompatible(diffs []Difference, p Policy) []Difference {
	var out []Difference
	for _, d := range diffs {
		if !IsCompatible(d.Type, p) {
			out = append(out, d)
		}
	}
	return out
}

type ctx struct {
	policy     Policy
	path       string
	diffs      []Difference
	inProgress map[[2]*Schema]bool
}

func (c *ctx) add(t string) { c.diffs = append(c.diffs, Difference{Type: t, Path: c.path}) }

func (c *ctx) addAt(seg, t string) {
	c.diffs = append(c.diffs, Difference{Type: t, Path: join(c.path, seg)})
}

func join(path, seg string) string {
	if path == "#/" {
		return path + seg
	}
	return path + "/" + seg
}

// at runs fn with the path extended by seg.
func (c *ctx) at(seg string, fn func()) {
	saved := c.path
	c.path = join(c.path, seg)
	fn()
	c.path = saved
}

func (c *ctx) sub() *ctx {
	return &ctx{policy: c.policy, path: c.path, inProgress: c.inProgress}
}

func (c *ctx) ok() bool {
	for _, d := range c.diffs {
		if !IsCompatible(d.Type, c.policy) {
			return false
		}
	}
	return true
}

func (c *ctx) merge(o *ctx) { c.diffs = append(c.diffs, o.diffs...) }

func (c *ctx) compare(o, u *Schema) {
	switch {
	case o == nil && u == nil:
		return
	case o == nil:
		c.add("SCHEMA_ADDED")
		return
	case u == nil:
		c.add("SCHEMA_REMOVED")
		return
	}
	key := [2]*Schema{o, u}
	if c.inProgress[key] {
		return // recursive schema: assume compatible while the outer pair is checked
	}
	c.inProgress[key] = true
	defer delete(c.inProgress, key)
	c.compareTypes(o, u)
}

func singleton(s *Schema) bool { return s.Kind == KCombined && len(dedupe(s.Subs)) == 1 }

func (c *ctx) compareTypes(o, u *Schema) {
	if o.Kind != KCombined && u.Kind == KCombined {
		if len(u.Subs) == 1 {
			s := c.sub()
			s.compare(o, u.Subs[0])
			if s.ok() {
				c.merge(s)
				return
			}
		} else if u.Criterion == "anyOf" || u.Criterion == "oneOf" {
			for _, branch := range u.Subs {
				s := c.sub()
				s.compare(o, branch)
				if s.ok() {
					c.merge(s)
					c.add("SUM_TYPE_EXTENDED")
					return
				}
			}
		}
	} else if o.Kind == KCombined && u.Kind != KCombined {
		if len(o.Subs) == 1 {
			s := c.sub()
			s.compare(o.Subs[0], u)
			if s.ok() {
				c.merge(s)
				return
			}
		} else if o.Criterion == "allOf" {
			for _, part := range o.Subs {
				s := c.sub()
				s.compare(part, u)
				if s.ok() {
					c.merge(s)
					c.add("PRODUCT_TYPE_NARROWED")
					return
				}
			}
		}
	}

	if o.Kind != u.Kind {
		if o.Kind == KFalse || u.Kind == KEmpty {
			return // nothing was valid before, or anything is valid now
		}
		c.add("TYPE_CHANGED")
		return
	}

	if !eqStr(o.ID, u.ID) {
		c.add("ID_CHANGED")
	}
	if !eqStr(o.Title, u.Title) {
		c.add("TITLE_CHANGED")
	}
	if !eqStr(o.Description, u.Description) {
		c.add("DESCRIPTION_CHANGED")
	}
	if o.HasDefault != u.HasDefault || o.Default != u.Default {
		c.add("DEFAULT_CHANGED")
	}

	switch o.Kind {
	case KString:
		c.compareString(o, u)
	case KNumber:
		c.compareNumber(o, u)
	case KConst:
		if o.Const != u.Const {
			c.addAt("const", "ENUM_ARRAY_CHANGED")
		}
	case KEnum:
		c.compareEnum(o, u)
	case KCombined:
		c.compareCombined(o, u)
	case KNot:
		c.at("not", func() {
			s := c.sub()
			s.compare(u.Not, o.Not) // reversed: the update must forbid less
			if s.ok() {
				c.add("NOT_TYPE_NARROWED")
			} else {
				c.add("NOT_TYPE_EXTENDED")
			}
		})
	case KObject:
		c.compareObject(o, u)
	case KArray:
		c.compareArray(o, u)
	}
}

func eqStr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// limit classifies a change to an optional numeric keyword.
func (c *ctx) limit(seg string, o, u *float64, base string) {
	switch {
	case o == nil && u == nil:
	case o == nil:
		c.addAt(seg, base+"_ADDED")
	case u == nil:
		c.addAt(seg, base+"_REMOVED")
	case *o < *u:
		c.addAt(seg, base+"_INCREASED")
	case *o > *u:
		c.addAt(seg, base+"_DECREASED")
	}
}

func (c *ctx) compareString(o, u *Schema) {
	c.limit("maxLength", o.MaxLength, u.MaxLength, "MAX_LENGTH")
	c.limit("minLength", o.MinLength, u.MinLength, "MIN_LENGTH")
	switch {
	case o.Pattern == nil && u.Pattern != nil:
		c.addAt("pattern", "PATTERN_ADDED")
	case o.Pattern != nil && u.Pattern == nil:
		c.addAt("pattern", "PATTERN_REMOVED")
	case o.Pattern != nil && *o.Pattern != *u.Pattern:
		c.addAt("pattern", "PATTERN_CHANGED")
	}
}

func isMultiple(a, b float64) bool {
	if b == 0 {
		return false
	}
	q := a / b
	return math.Abs(q-math.Round(q)) < 1e-9
}

func (c *ctx) compareNumber(o, u *Schema) {
	c.limit("maximum", o.Maximum, u.Maximum, "MAXIMUM")
	c.limit("minimum", o.Minimum, u.Minimum, "MINIMUM")
	c.limit("exclusiveMaximum", o.ExclusiveMaximum, u.ExclusiveMaximum, "EXCLUSIVE_MAXIMUM")
	c.limit("exclusiveMinimum", o.ExclusiveMinimum, u.ExclusiveMinimum, "EXCLUSIVE_MINIMUM")
	om, um := o.MultipleOf, u.MultipleOf
	switch {
	case om == nil && um == nil:
	case om == nil:
		c.addAt("multipleOf", "MULTIPLE_OF_ADDED")
	case um == nil:
		c.addAt("multipleOf", "MULTIPLE_OF_REMOVED")
	case *om == *um:
	case isMultiple(*um, *om):
		c.addAt("multipleOf", "MULTIPLE_OF_EXPANDED")
	case isMultiple(*om, *um):
		c.addAt("multipleOf", "MULTIPLE_OF_REDUCED")
	default:
		c.addAt("multipleOf", "MULTIPLE_OF_CHANGED")
	}
	if o.Integer != u.Integer {
		if o.Integer {
			c.add("TYPE_EXTENDED")
		} else {
			c.add("TYPE_NARROWED")
		}
	}
}

func containsAll(set, sub []string) bool {
	for _, s := range sub {
		if !slices.Contains(set, s) {
			return false
		}
	}
	return true
}

func (c *ctx) compareEnum(o, u *Schema) {
	if slices.Equal(o.Enum, u.Enum) {
		return
	}
	switch {
	case containsAll(u.Enum, o.Enum):
		c.addAt("enum", "ENUM_ARRAY_EXTENDED")
	case containsAll(o.Enum, u.Enum):
		c.addAt("enum", "ENUM_ARRAY_NARROWED")
	default:
		c.addAt("enum", "ENUM_ARRAY_CHANGED")
	}
}

func dedupe(subs []*Schema) []*Schema {
	seen := map[string]bool{}
	var out []*Schema
	for _, s := range subs {
		if seen[s.key] {
			continue
		}
		seen[s.key] = true
		out = append(out, s)
	}
	return out
}

func (c *ctx) compareCombined(o, u *Schema) {
	if o.Criterion != u.Criterion {
		if u.Criterion == "anyOf" ||
			(singleton(o) && singleton(u)) ||
			(singleton(o) && u.Criterion == "oneOf") ||
			(singleton(u) && o.Criterion == "allOf") {
			c.add("COMBINED_TYPE_EXTENDED")
		} else {
			c.add("COMBINED_TYPE_CHANGED")
			return
		}
	}
	os, us := dedupe(o.Subs), dedupe(u.Subs)
	switch {
	case len(os) < len(us):
		if u.Criterion == "allOf" {
			c.add("PRODUCT_TYPE_EXTENDED")
		} else {
			c.add("SUM_TYPE_EXTENDED")
		}
	case len(os) > len(us):
		if o.Criterion == "anyOf" || o.Criterion == "oneOf" {
			c.add("SUM_TYPE_NARROWED")
		} else {
			c.add("PRODUCT_TYPE_NARROWED")
		}
	}

	// Pair each original branch with a compatible update branch using a
	// maximum bipartite matching, so reordering branches is not a change.
	edges := make([][]*ctx, len(os))
	for i := range os {
		edges[i] = make([]*ctx, len(us))
		c.at(o.Criterion+"/"+strconv.Itoa(i), func() {
			for j := range us {
				s := c.sub()
				s.compare(os[i], us[j])
				if s.ok() {
					edges[i][j] = s
				}
			}
		})
	}
	match := maxMatching(edges, len(us))
	matched := 0
	for i, j := range match {
		if j >= 0 {
			matched++
			c.merge(edges[i][j])
		}
	}
	if matched < min(len(os), len(us)) {
		c.add("COMBINED_TYPE_SUBSCHEMAS_CHANGED")
	}
}

// maxMatching returns, for each left node, the matched right node or -1
// (Kuhn's augmenting-path algorithm; branch counts are tiny).
func maxMatching(edges [][]*ctx, right int) []int {
	matchR := make([]int, right)
	for i := range matchR {
		matchR[i] = -1
	}
	var try func(l int, seen []bool) bool
	try = func(l int, seen []bool) bool {
		for r := 0; r < right; r++ {
			if edges[l][r] == nil || seen[r] {
				continue
			}
			seen[r] = true
			if matchR[r] < 0 || try(matchR[r], seen) {
				matchR[r] = l
				return true
			}
		}
		return false
	}
	for l := range edges {
		try(l, make([]bool, right))
	}
	matchL := make([]int, len(edges))
	for i := range matchL {
		matchL[i] = -1
	}
	for r, l := range matchR {
		if l >= 0 {
			matchL[l] = r
		}
	}
	return matchL
}

func openObject(s *Schema) bool {
	return s.AddlPermits && s.AddlSchema == nil && len(s.PatternProps) == 0
}

func partialObject(s *Schema, prop string) *Schema {
	for _, pp := range s.PatternProps {
		if pp.Re != nil && pp.Re.MatchString(prop) {
			return pp.Schema
		}
	}
	return s.AddlSchema
}

func sortedKeys[V any](maps ...map[string]V) []string {
	seen := map[string]bool{}
	var keys []string
	for _, m := range maps {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	slices.Sort(keys)
	return keys
}

func escape(seg string) string {
	return strings.ReplaceAll(strings.ReplaceAll(seg, "~", "~0"), "/", "~1")
}

func (c *ctx) compareObject(o, u *Schema) {
	// required
	c.at("required", func() {
		for _, k := range sortedKeys(o.Properties) {
			up, inUpdate := u.Properties[k]
			if !inUpdate {
				continue
			}
			switch {
			case o.Required[k] && !u.Required[k]:
				c.addAt(escape(k), "REQUIRED_ATTRIBUTE_REMOVED")
			case !o.Required[k] && u.Required[k]:
				if up.HasDefault {
					c.addAt(escape(k), "REQUIRED_ATTRIBUTE_WITH_DEFAULT_ADDED")
				} else {
					c.addAt(escape(k), "REQUIRED_ATTRIBUTE_ADDED")
				}
			}
		}
	})
	// properties
	c.at("properties", func() {
		for _, k := range sortedKeys(o.Properties, u.Properties) {
			c.at(escape(k), func() { c.compareProperty(o, u, k) })
		}
	})
	// dependencies
	c.at("dependencies", func() {
		for _, k := range sortedKeys(o.PropDeps, u.PropDeps) {
			od, inO := o.PropDeps[k]
			ud, inU := u.PropDeps[k]
			switch {
			case !inU:
				c.addAt(escape(k), "DEPENDENCY_ARRAY_REMOVED")
			case !inO:
				c.addAt(escape(k), "DEPENDENCY_ARRAY_ADDED")
			case slices.Equal(od, ud):
			case containsAll(ud, od):
				c.addAt(escape(k), "DEPENDENCY_ARRAY_EXTENDED")
			case containsAll(od, ud):
				c.addAt(escape(k), "DEPENDENCY_ARRAY_NARROWED")
			default:
				c.addAt(escape(k), "DEPENDENCY_ARRAY_CHANGED")
			}
		}
		for _, k := range sortedKeys(o.SchemaDeps, u.SchemaDeps) {
			od, ud := o.SchemaDeps[k], u.SchemaDeps[k]
			switch {
			case ud == nil:
				c.addAt(escape(k), "DEPENDENCY_SCHEMA_REMOVED")
			case od == nil:
				c.addAt(escape(k), "DEPENDENCY_SCHEMA_ADDED")
			default:
				c.at(escape(k), func() { c.compare(od, ud) })
			}
		}
	})
	// additionalProperties
	c.at("additionalProperties", func() {
		switch {
		case o.AddlPermits != u.AddlPermits:
			if u.AddlPermits {
				c.add("ADDITIONAL_PROPERTIES_ADDED")
			} else {
				c.add("ADDITIONAL_PROPERTIES_REMOVED")
			}
		case o.AddlSchema == nil && u.AddlSchema != nil:
			c.add("ADDITIONAL_PROPERTIES_NARROWED")
		case o.AddlSchema != nil && u.AddlSchema == nil:
			c.add("ADDITIONAL_PROPERTIES_EXTENDED")
		case o.AddlSchema != nil:
			c.compare(o.AddlSchema, u.AddlSchema)
		}
	})
	c.limit("maxProperties", o.MaxProps, u.MaxProps, "MAX_PROPERTIES")
	c.limit("minProperties", o.MinProps, u.MinProps, "MIN_PROPERTIES")
}

func (c *ctx) compareProperty(o, u *Schema, k string) {
	op, up := o.Properties[k], u.Properties[k]
	switch {
	case up == nil: // removed
		if openObject(u) {
			c.add("PROPERTY_REMOVED_FROM_OPEN_CONTENT_MODEL")
			return
		}
		if partial := partialObject(u, k); partial != nil {
			s := c.sub()
			s.compare(op, partial)
			c.merge(s)
			if s.ok() {
				c.add("PROPERTY_REMOVED_IS_COVERED_BY_PARTIALLY_OPEN_CONTENT_MODEL")
			} else {
				c.add("PROPERTY_REMOVED_NOT_COVERED_BY_PARTIALLY_OPEN_CONTENT_MODEL")
			}
			return
		}
		if op.Kind == KFalse {
			c.add("PROPERTY_WITH_FALSE_REMOVED_FROM_CLOSED_CONTENT_MODEL")
		} else {
			c.add("PROPERTY_REMOVED_FROM_CLOSED_CONTENT_MODEL")
		}
	case op == nil: // added
		if openObject(o) {
			switch {
			case up.Kind == KEmpty:
				c.add("PROPERTY_WITH_EMPTY_SCHEMA_ADDED_TO_OPEN_CONTENT_MODEL")
			case u.Required[k] && up.HasDefault:
				c.add("REQUIRED_PROPERTY_WITH_DEFAULT_ADDED_TO_OPEN_CONTENT_MODEL")
			case u.Required[k]:
				c.add("REQUIRED_PROPERTY_ADDED_TO_OPEN_CONTENT_MODEL")
			default:
				c.add("OPTIONAL_PROPERTY_ADDED_TO_OPEN_CONTENT_MODEL")
			}
			return
		}
		if partial := partialObject(o, k); partial != nil {
			s := c.sub()
			s.compare(partial, up)
			c.merge(s)
			if s.ok() {
				c.add("PROPERTY_ADDED_IS_COVERED_BY_PARTIALLY_OPEN_CONTENT_MODEL")
			} else {
				c.add("PROPERTY_ADDED_NOT_COVERED_BY_PARTIALLY_OPEN_CONTENT_MODEL")
			}
		}
		switch {
		case u.Required[k] && up.HasDefault:
			c.add("REQUIRED_PROPERTY_WITH_DEFAULT_ADDED_TO_UNOPEN_CONTENT_MODEL")
		case u.Required[k]:
			c.add("REQUIRED_PROPERTY_ADDED_TO_UNOPEN_CONTENT_MODEL")
		default:
			c.add("OPTIONAL_PROPERTY_ADDED_TO_UNOPEN_CONTENT_MODEL")
		}
	default:
		c.compare(op, up)
	}
}

func openArray(s *Schema) bool { return s.AddlItemsPermits && s.AddlItemsSchema == nil }

func (c *ctx) compareArray(o, u *Schema) {
	c.at("items", func() { c.compare(o.AllItems, u.AllItems) })

	n := min(len(o.ItemList), len(u.ItemList))
	for i := 0; i < n; i++ {
		c.at("items/"+strconv.Itoa(i), func() { c.compare(o.ItemList[i], u.ItemList[i]) })
	}
	for i := n; i < len(o.ItemList); i++ { // removed tuple items
		c.at("items/"+strconv.Itoa(i), func() {
			oi := o.ItemList[i]
			switch {
			case openArray(u):
				c.add("ITEM_REMOVED_FROM_OPEN_CONTENT_MODEL")
			case u.AddlItemsSchema != nil:
				s := c.sub()
				s.compare(oi, u.AddlItemsSchema)
				c.merge(s)
				if s.ok() {
					c.add("ITEM_REMOVED_IS_COVERED_BY_PARTIALLY_OPEN_CONTENT_MODEL")
				} else {
					c.add("ITEM_REMOVED_NOT_COVERED_BY_PARTIALLY_OPEN_CONTENT_MODEL")
				}
			case oi.Kind == KFalse:
				c.add("ITEM_WITH_FALSE_REMOVED_FROM_CLOSED_CONTENT_MODEL")
			default:
				c.add("ITEM_REMOVED_FROM_CLOSED_CONTENT_MODEL")
			}
		})
	}
	for i := n; i < len(u.ItemList); i++ { // added tuple items
		c.at("items/"+strconv.Itoa(i), func() {
			ui := u.ItemList[i]
			switch {
			case openArray(o):
				if ui.Kind == KEmpty {
					c.add("ITEM_WITH_EMPTY_SCHEMA_ADDED_TO_OPEN_CONTENT_MODEL")
				} else {
					c.add("ITEM_ADDED_TO_OPEN_CONTENT_MODEL")
				}
			case o.AddlItemsSchema != nil:
				s := c.sub()
				s.compare(o.AddlItemsSchema, ui)
				c.merge(s)
				if s.ok() {
					c.add("ITEM_ADDED_IS_COVERED_BY_PARTIALLY_OPEN_CONTENT_MODEL")
				} else {
					c.add("ITEM_ADDED_NOT_COVERED_BY_PARTIALLY_OPEN_CONTENT_MODEL")
				}
			default:
				c.add("ITEM_ADDED_TO_CLOSED_CONTENT_MODEL")
			}
		})
	}
	c.at("additionalItems", func() {
		switch {
		case o.AddlItemsPermits != u.AddlItemsPermits:
			if o.AddlItemsPermits {
				c.add("ADDITIONAL_ITEMS_REMOVED")
			} else {
				c.add("ADDITIONAL_ITEMS_ADDED")
			}
		case o.AddlItemsSchema == nil && u.AddlItemsSchema != nil:
			c.add("ADDITIONAL_ITEMS_NARROWED")
		case o.AddlItemsSchema != nil && u.AddlItemsSchema == nil:
			c.add("ADDITIONAL_ITEMS_EXTENDED")
		case o.AddlItemsSchema != nil:
			c.compare(o.AddlItemsSchema, u.AddlItemsSchema)
		}
	})
	c.limit("maxItems", o.MaxItems, u.MaxItems, "MAX_ITEMS")
	c.limit("minItems", o.MinItems, u.MinItems, "MIN_ITEMS")
	if o.Unique != u.Unique {
		if o.Unique {
			c.addAt("uniqueItems", "UNIQUE_ITEMS_REMOVED")
		} else {
			c.addAt("uniqueItems", "UNIQUE_ITEMS_ADDED")
		}
	}
}
