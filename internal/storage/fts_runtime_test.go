package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestFTSMetadataAndNoopUpdatesDoNotRewriteIndex(t *testing.T) {
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "fts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO observations(id,title,content,obs_type,tags) VALUES('o','oldtitle','oldcontent','context','["oldtag"]')`,
		`INSERT INTO skills(id,name,description,procedure,tags) VALUES('s','oldname','olddescription','oldprocedure','["oldskilltag"]')`,
	} {
		if _, err := db.sql.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ table, word string }{{"observations", "oldtitle"}, {"skills", "oldname"}} {
		var n int
		if err := db.sql.QueryRow(`SELECT count(*) FROM `+tc.table+`_fts WHERE `+tc.table+`_fts MATCH ?`, tc.word).Scan(&n); err != nil || n != 1 {
			t.Fatalf("insert not indexed in %s: %d %v", tc.table, n, err)
		}
	}
	for _, q := range []string{
		`UPDATE observations SET access_count=access_count+1,last_accessed_at=CURRENT_TIMESTAMP WHERE id='o'`,
		`UPDATE observations SET pinned=1,trust_tier='raw',last_exported_at=CURRENT_TIMESTAMP WHERE id='o'`,
		`UPDATE observations SET invalidated_at=CURRENT_TIMESTAMP,importance=4 WHERE id='o'`,
		`UPDATE observations SET embedding=X'0000803F',embedding_model='fixture' WHERE id='o'`,
		`UPDATE observations SET title=title,content=content,tags=tags WHERE id='o'`,
		`UPDATE skills SET use_count=use_count+1,effectiveness=0.5,last_exported_at=CURRENT_TIMESTAMP WHERE id='s'`,
		`UPDATE skills SET name=name,description=description,procedure=procedure,tags=tags WHERE id='s'`,
	} {
		var before, after int64
		if err := db.sql.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		if _, err := db.sql.Exec(q); err != nil {
			t.Fatal(err)
		}
		if err := db.sql.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if after-before != 1 {
			t.Errorf("metadata/no-op updated %d rows including index writes, want base row only: %s", after-before, q)
		}
	}
	for _, tc := range []struct{ table, column, old, next string }{
		{"observations", "title", "oldtitle", "newtitle"}, {"observations", "content", "oldcontent", "newcontent"}, {"observations", "tags", "oldtag", "newtag"},
		{"skills", "name", "oldname", "newname"}, {"skills", "description", "olddescription", "newdescription"}, {"skills", "procedure", "oldprocedure", "newprocedure"}, {"skills", "tags", "oldskilltag", "newskilltag"},
	} {
		value := tc.next
		if tc.column == "tags" {
			value = `["` + value + `"]`
		}
		if _, err := db.sql.Exec(`UPDATE `+tc.table+` SET `+tc.column+`=?`, value); err != nil {
			t.Fatal(err)
		}
		for _, word := range []string{tc.old, tc.next} {
			var n int
			if err := db.sql.QueryRow(`SELECT count(*) FROM `+tc.table+`_fts WHERE `+tc.table+`_fts MATCH ?`, word).Scan(&n); err != nil {
				t.Fatal(err)
			}
			want := 0
			if word == tc.next {
				want = 1
			}
			if n != want {
				t.Errorf("%s index %s matches=%d want %d", tc.table, word, n, want)
			}
		}
	}
	for _, table := range []string{"observations", "skills"} {
		if _, err := db.sql.Exec(`DELETE FROM ` + table); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := db.sql.QueryRow(`SELECT count(*) FROM ` + table + `_fts WHERE ` + table + `_fts MATCH 'new*'`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("delete left indexed text: %d %v", n, err)
		}
	}
}
