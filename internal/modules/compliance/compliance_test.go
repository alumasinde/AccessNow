package compliance

import "testing"

// TestCategoryVocabulary locks the five categories this module can actually purge. The schema
// accepts more, and a policy for one of those would be a promise the purge cannot keep.
func TestCategoryVocabulary(t *testing.T) {
	valid := []Category{
		CategoryVisitorIdentity, CategoryVisitorPhoto, CategoryPassDocument,
		CategoryAttachment, CategoryNotifyAddress,
	}
	for _, category := range valid {
		if !category.Valid() {
			t.Fatalf("%s is a supported category but was refused", category)
		}
		if category.entityType() == "" {
			t.Fatalf("%s has no hold entity type", category)
		}
	}
	for _, category := range []Category{"audit_log", "report_file", "everything"} {
		if category.Valid() {
			t.Fatalf("%s must not be accepted as a purge category", category)
		}
	}
}

func TestOnlyIdentifierCategoriesCanBeAnonymised(t *testing.T) {
	if !CategoryVisitorIdentity.anonable() || !CategoryPassDocument.anonable() {
		t.Fatal("identifier categories should support anonymise")
	}
	for _, category := range []Category{CategoryVisitorPhoto, CategoryAttachment,
		CategoryNotifyAddress} {
		if category.anonable() {
			t.Fatalf("%s has no value to hash and must not accept anonymise", category)
		}
	}
}

func TestHoldEntityTypesAreClosed(t *testing.T) {
	for _, entity := range []string{"passes", "visitors", "gate_movements", "notifications",
		"staff", "vehicles", "contractor_documents", "pass_attachments", "visits"} {
		if !knownEntityType(entity) {
			t.Fatalf("%s should be holdable", entity)
		}
	}
	for _, entity := range []string{"", "everything", "audit_logs; DROP TABLE passes"} {
		if knownEntityType(entity) {
			t.Fatalf("%q must not be accepted as a hold entity", entity)
		}
	}
}

func TestPurgeActions(t *testing.T) {
	for _, action := range []PurgeAction{ActionNull, ActionAnonymise, ActionDelete, ActionArchive} {
		if !action.Valid() {
			t.Fatalf("%q is a supported action but was refused", action)
		}
	}
	for _, action := range []PurgeAction{"", "drop", "NULL"} {
		if action.Valid() {
			t.Fatalf("%q must not be accepted", action)
		}
	}

	// delete is only honest where the row and the data are the same thing.
	if !CategoryAttachment.Supports(ActionDelete) {
		t.Fatal("an attachment is the one category whose row can be deleted")
	}
	for _, category := range []Category{CategoryVisitorIdentity, CategoryVisitorPhoto,
		CategoryPassDocument, CategoryNotifyAddress} {
		if category.Supports(ActionDelete) {
			t.Fatalf("%s cannot be deleted without taking its record and trail with it", category)
		}
		if !category.Supports(ActionArchive) {
			t.Fatalf("%s should support archive", category)
		}
	}
	if CategoryVisitorPhoto.Supports(ActionAnonymise) ||
		!CategoryVisitorIdentity.Supports(ActionAnonymise) {
		t.Fatal("anonymise belongs to the identifier categories only")
	}
	if !CategoryAttachment.Supports(ActionNull) || !CategoryAttachment.Supports(ActionArchive) {
		t.Fatal("attachment should support null and archive")
	}
	if Category("audit_log").Supports(ActionNull) {
		t.Fatal("a category the purge does not implement must support nothing")
	}
}

// TestCategoryHoldMapping keeps the category and the hold lookup in step: a hold filed against
// one table must be found when a purge of the matching category asks about the row.
func TestCategoryHoldMapping(t *testing.T) {
	want := map[Category]string{
		CategoryVisitorIdentity: "visitors",
		CategoryVisitorPhoto:    "visitors",
		CategoryPassDocument:    "passes",
		CategoryAttachment:      "pass_attachments",
		CategoryNotifyAddress:   "notifications",
	}
	for category, entity := range want {
		if got := category.entityType(); got != entity {
			t.Fatalf("%s maps to %s, want %s", category, got, entity)
		}
	}
}
