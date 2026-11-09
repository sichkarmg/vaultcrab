// SPDX-License-Identifier: MIT

package vault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// HostEdit is the editable part of a host item. Password is nil to keep the
// current one; it is wiped by Create/Update.
type HostEdit struct {
	Name       string
	Scheme     model.Scheme
	User, Addr string
	Port       int // 0 = default port of the scheme
	Path       string
	Tags       []string
	FolderID   string // "" = no folder
	// OrgID and CollectionIDs (required with OrgID): the organization and
	// collection of a new item. On update: a personal item is moved into the
	// organization; an item of that organization gets these collections
	// instead of its own; "" keeps the item where it is. Bitwarden cannot move
	// an item out of its organization.
	OrgID         string
	CollectionIDs []string
	// CopyOf is the ID of an item to copy (create only): the new item gets its
	// content (password, notes, custom and hidden fields, TOTP) with the edit
	// applied; the password is kept unless Password is set.
	CopyOf   string
	Password []byte
	// Notes replaces the notes of the item ("" removes them); nil keeps them.
	Notes *string
	// Knock replaces the port knocking sequence (the field knock; ""
	// removes it); nil keeps it.
	Knock *string
}

// Validate checks the required fields.
func (e *HostEdit) Validate() error {
	switch {
	case strings.TrimSpace(e.Name) == "":
		return errors.New("name is required")
	case strings.TrimSpace(e.Addr) == "":
		return errors.New("host is required")
	case strings.ContainsAny(e.Addr, " /@"):
		return errors.New("host must be a name or an address")
	case e.Port < 0 || e.Port > 65535:
		return errors.New("port must be 1-65535")
	case e.OrgID != "" && len(e.CollectionIDs) == 0:
		return errors.New("an organization item needs a collection")
	}
	if _, ok := model.ParseScheme(string(e.Scheme)); !ok {
		return fmt.Errorf("unknown type %q", e.Scheme)
	}
	return nil
}

// userHost formats user@host[:port] like the existing entries.
func (e *HostEdit) userHost() string {
	h := e.Addr
	if e.Port != 0 && e.Port != e.Scheme.DefaultPort() {
		h = net.JoinHostPort(e.Addr, strconv.Itoa(e.Port))
	} else if strings.Contains(h, ":") {
		h = "[" + h + "]"
	}
	if e.User == "" {
		return h
	}
	return e.User + "@" + h
}

func (e *HostEdit) uri() string {
	u := url.URL{Scheme: string(e.Scheme), Host: e.Addr, Path: "/" + strings.TrimPrefix(e.Path, "/")}
	if e.Port != 0 && e.Port != e.Scheme.DefaultPort() {
		u.Host = net.JoinHostPort(e.Addr, strconv.Itoa(e.Port))
	} else if strings.Contains(e.Addr, ":") {
		u.Host = "[" + e.Addr + "]"
	}
	return u.String()
}

// apply writes the edit into the raw item JSON (all other content of the
// item is kept byte for byte). The format follows the item's: a URI with the
// scheme (username = user) if it has one or a path or no user is given,
// otherwise the main format (boolean field named after the scheme, username user@host).
func (e *HostEdit) apply(item map[string]json.RawMessage) error {
	login := map[string]json.RawMessage{}
	defer wipeMap(login)
	if raw := item["login"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &login); err != nil {
			return fmt.Errorf("item login: %w", err)
		}
	}
	var uris []json.RawMessage
	if raw := login["uris"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &uris); err != nil {
			return fmt.Errorf("item uris: %w", err)
		}
	}
	var fields []json.RawMessage
	if raw := item["fields"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &fields); err != nil {
			return fmt.Errorf("item fields: %w", err)
		}
		defer func() {
			for _, f := range fields {
				secret.Zero(f)
			}
		}()
	}

	// URI of a known scheme already present?
	uriAt := -1
	for i, raw := range uris {
		var u struct {
			URI string `json:"uri"`
		}
		if json.Unmarshal(raw, &u) == nil {
			if pu, err := url.Parse(strings.TrimSpace(u.URI)); err == nil && pu.Host != "" {
				if _, ok := model.ParseScheme(pu.Scheme); ok {
					uriAt = i
					break
				}
			}
		}
	}
	// without a user, "host" in the username would read as a user name
	useURI := uriAt >= 0 || e.Path != "" || e.User == ""
	username := e.userHost()
	if useURI {
		username = e.User
		u, _ := json.Marshal(map[string]any{"match": nil, "uri": e.uri()})
		if uriAt >= 0 {
			uris[uriAt] = u
		} else {
			uris = append(uris, u)
		}
	}

	// Fields: drop our scheme flags, port and tags, keep everything else
	// (hidden fields included) as is, then add ours.
	kept := fields[:0:0]
	for _, raw := range fields {
		var f struct {
			Name string `json:"name"`
			Type int    `json:"type"`
		}
		_ = json.Unmarshal(raw, &f)
		name := strings.ToLower(strings.TrimSpace(f.Name))
		if f.Type != fieldHidden {
			if _, ok := model.ParseScheme(name); ok || name == "port" || name == "tags" || name == "tag" {
				continue
			}
			if name == "knock" && e.Knock != nil {
				continue
			}
		}
		kept = append(kept, raw)
	}
	add := func(name, value string, typ int) {
		b, _ := json.Marshal(map[string]any{"name": name, "value": value, "type": typ, "linkedId": nil})
		kept = append(kept, b)
	}
	if !useURI {
		add(string(e.Scheme), "true", fieldBoolean)
	}
	if len(e.Tags) > 0 {
		add("tags", strings.Join(e.Tags, ","), fieldText)
	}
	if e.Knock != nil && strings.TrimSpace(*e.Knock) != "" {
		add("knock", strings.TrimSpace(*e.Knock), fieldText)
	}

	set := func(m map[string]json.RawMessage, k string, v any) {
		b, _ := json.Marshal(v)
		secret.Zero(m[k]) // the old value may hold secrets (hidden fields, password)
		m[k] = b
	}
	set(item, "name", strings.TrimSpace(e.Name))
	set(item, "type", itemTypeLogin)
	if e.FolderID == "" {
		item["folderId"] = json.RawMessage("null")
	} else {
		set(item, "folderId", e.FolderID)
	}
	set(item, "fields", kept)
	if e.Notes != nil {
		if *e.Notes == "" {
			secret.Zero(item["notes"])
			item["notes"] = json.RawMessage("null")
		} else {
			set(item, "notes", *e.Notes)
		}
	}
	set(login, "username", username)
	set(login, "uris", uris)
	if e.Password != nil {
		secret.Zero(login["password"])
		login["password"] = appendJSONString(nil, e.Password) // wiped with the login map
	}
	lb, err := json.Marshal(login)
	if err != nil {
		return err
	}
	secret.Zero(item["login"])
	item["login"] = lb
	return nil
}

func wipeMap(m map[string]json.RawMessage) {
	for _, v := range m {
		secret.Zero(v)
	}
}

// CreateHost creates a host item (a copy of e.CopyOf, if set) and returns
// its ID.
func (c *Client) CreateHost(ctx context.Context, e HostEdit) (string, error) {
	defer secret.Zero(e.Password)
	if err := e.Validate(); err != nil {
		return "", err
	}
	item := map[string]json.RawMessage{
		"notes": json.RawMessage("null"), "favorite": json.RawMessage("false"),
		"reprompt": json.RawMessage("0"),
	}
	if e.CopyOf != "" {
		data, err := c.do(ctx, http.MethodGet, "/object/item/"+url.PathEscape(e.CopyOf), nil)
		if err != nil {
			return "", err
		}
		item = map[string]json.RawMessage{}
		err = json.Unmarshal(data, &item)
		secret.Zero(data)
		if err != nil {
			return "", errors.New("bw: invalid item")
		}
		// a new item: no identity, history or attachments of the original
		for _, k := range []string{"id", "object", "revisionDate", "creationDate", "deletedDate", "passwordHistory", "attachments"} {
			secret.Zero(item[k])
			delete(item, k)
		}
	} else if e.Password == nil {
		e.Password = []byte{}
	}
	item["organizationId"], item["collectionIds"] = json.RawMessage("null"), json.RawMessage("[]")
	if e.OrgID != "" {
		item["organizationId"], _ = json.Marshal(e.OrgID)
		item["collectionIds"], _ = json.Marshal(e.CollectionIDs)
	}
	if err := e.apply(item); err != nil {
		return "", err
	}
	body, err := json.Marshal(item)
	wipeMap(item)
	if err != nil {
		return "", err
	}
	defer secret.Zero(body)
	data, err := c.do(ctx, http.MethodPost, "/object/item", body)
	if err != nil {
		return "", err
	}
	defer secret.Zero(data)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &created); err != nil || created.ID == "" {
		return "", errors.New("bw: unexpected response to create item")
	}
	c.markCreated(created.ID)
	return created.ID, nil
}

// UpdateHost changes the host fields of an existing item and keeps the rest
// of it (notes, hidden fields, TOTP, password history, attachments, ...).
// The password is changed only if e.Password is not nil.
func (c *Client) UpdateHost(ctx context.Context, id string, e HostEdit) error {
	defer secret.Zero(e.Password)
	if err := e.Validate(); err != nil {
		return err
	}
	path := "/object/item/" + url.PathEscape(id)
	data, err := c.do(ctx, http.MethodGet, path, nil) // the full item, secrets included
	if err != nil {
		return err
	}
	defer secret.Zero(data)
	item := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &item); err != nil {
		return errors.New("bw: invalid item")
	}
	defer wipeMap(item)
	if t := strings.TrimSpace(string(item["type"])); t != strconv.Itoa(itemTypeLogin) {
		return errors.New("only login items can be edited")
	}
	var curOrg string
	var curCols []string
	_ = json.Unmarshal(item["organizationId"], &curOrg)
	_ = json.Unmarshal(item["collectionIds"], &curCols)
	if e.OrgID != "" && curOrg != "" && curOrg != e.OrgID {
		return errors.New("Bitwarden cannot move an item to another organization")
	}
	if err := e.apply(item); err != nil {
		return err
	}
	body, err := json.Marshal(item)
	if err != nil {
		return err
	}
	defer secret.Zero(body)
	resp, err := c.do(ctx, http.MethodPut, path, body)
	secret.Zero(resp)
	if err != nil || e.OrgID == "" {
		return err
	}
	move, recollect := curOrg == "", curOrg != "" && !sameSet(curCols, e.CollectionIDs)
	if !move && !recollect {
		return nil
	}
	// bw refuses to move its copy of the item after the update ("out of
	// date") until it has synced
	if err := c.Sync(ctx); err != nil {
		return err
	}
	cols, _ := json.Marshal(e.CollectionIDs)
	switch {
	case move: // a personal item into the organization
		resp, err = c.do(ctx, http.MethodPost, "/move/"+url.PathEscape(id)+"/"+url.PathEscape(e.OrgID), cols)
	default:
		resp, err = c.do(ctx, http.MethodPut, "/object/item-collections/"+url.PathEscape(id), cols)
	}
	secret.Zero(resp)
	return err
}

// sameSet reports whether a and b hold the same strings.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	have := map[string]bool{}
	for _, x := range a {
		have[x] = true
	}
	for _, x := range b {
		if !have[x] {
			return false
		}
	}
	return true
}

// CreateFolder creates a folder (nested folders use "/" in the name) and
// returns its ID.
func (c *Client) CreateFolder(ctx context.Context, name string) (string, error) {
	body, _ := json.Marshal(map[string]string{"name": name})
	data, err := c.do(ctx, http.MethodPost, "/object/folder", body)
	if err != nil {
		return "", err
	}
	var f struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &f); err != nil || f.ID == "" {
		return "", errors.New("bw: unexpected response to create folder")
	}
	return f.ID, nil
}

// DeleteItem moves an item to the trash (bw delete without --permanent).
func (c *Client) DeleteItem(ctx context.Context, id string) error {
	_, err := c.do(ctx, http.MethodDelete, "/object/item/"+url.PathEscape(id), nil)
	return err
}
