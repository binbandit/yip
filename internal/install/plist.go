package install

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Keep every plist value, including settings that yip service install does not
// generate. Only the executable path is changed during an upgrade.
type plistNode struct {
	XMLName xml.Name
	Attrs   []xml.Attr  `xml:",any,attr"`
	Text    string      `xml:",chardata"`
	Nodes   []plistNode `xml:",any"`
}

func parsePlist(data []byte) (*plistNode, error) {
	var root plistNode
	d := xml.NewDecoder(bytes.NewReader(data))
	if err := d.Decode(&root); err != nil {
		return nil, fmt.Errorf("expected an XML property list: %w", err)
	}
	for {
		t, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if c, ok := t.(xml.CharData); !ok || strings.TrimSpace(string(c)) != "" {
			return nil, errors.New("unexpected content after property list")
		}
	}
	if root.XMLName.Local != "plist" || len(root.Nodes) != 1 || root.Nodes[0].XMLName.Local != "dict" {
		return nil, errors.New("expected one plist dictionary")
	}
	if err := validPlist(&root); err != nil {
		return nil, err
	}
	return &root, nil
}

func validPlist(n *plistNode) error {
	if n.XMLName.Space != "" {
		return errors.New("namespaced property lists are unsupported")
	}
	for _, attr := range n.Attrs {
		if n.XMLName.Local != "plist" || attr.Name.Local != "version" || attr.Name.Space != "" || attr.Value != "1.0" {
			return errors.New("unsupported property list attribute")
		}
	}
	switch n.XMLName.Local {
	case "plist", "array", "dict":
		if strings.TrimSpace(n.Text) != "" {
			return errors.New("unexpected property list text")
		}
		if n.XMLName.Local == "dict" {
			if len(n.Nodes)%2 != 0 {
				return errors.New("unpaired dictionary key")
			}
			seen := map[string]bool{}
			for i := 0; i < len(n.Nodes); i += 2 {
				key := n.Nodes[i]
				if key.XMLName.Local != "key" || seen[key.Text] {
					return errors.New("invalid or duplicate dictionary key")
				}
				seen[key.Text] = true
			}
		}
		for i := range n.Nodes {
			child := n.Nodes[i].XMLName.Local
			if child == "plist" || (child == "key" && (n.XMLName.Local != "dict" || i%2 != 0)) {
				return errors.New("unexpected property list key or root")
			}
			if err := validPlist(&n.Nodes[i]); err != nil {
				return err
			}
		}
	case "true", "false":
		if strings.TrimSpace(n.Text) != "" || len(n.Nodes) != 0 {
			return errors.New("invalid plist boolean")
		}
	case "string", "key", "integer", "real", "date", "data":
		if len(n.Nodes) != 0 {
			return errors.New("invalid plist scalar")
		}
		var err error
		switch n.XMLName.Local {
		case "integer":
			_, err = strconv.ParseInt(strings.TrimSpace(n.Text), 10, 64)
		case "real":
			_, err = strconv.ParseFloat(strings.TrimSpace(n.Text), 64)
		case "date":
			_, err = time.Parse(time.RFC3339, strings.TrimSpace(n.Text))
		case "data":
			_, err = base64.StdEncoding.DecodeString(strings.Join(strings.Fields(n.Text), ""))
		}
		if err != nil {
			return fmt.Errorf("invalid plist %s: %w", n.XMLName.Local, err)
		}
	default:
		return fmt.Errorf("unsupported plist element %q", n.XMLName.Local)
	}
	return nil
}

func (n *plistNode) get(key string) *plistNode {
	for i := 0; i+1 < len(n.Nodes); i += 2 {
		if n.Nodes[i].Text == key {
			return &n.Nodes[i+1]
		}
	}
	return nil
}

func updatePlist(data []byte, label, destination string, verify func(string) error) ([]byte, bool, error) {
	root, err := parsePlist(data)
	if err != nil {
		return nil, false, err
	}
	dict := &root.Nodes[0]
	l := dict.get("Label")
	if l == nil || l.XMLName.Local != "string" || l.Text != label {
		return nil, false, errors.New("service label does not match its filename")
	}
	args := dict.get("ProgramArguments")
	if args == nil || args.XMLName.Local != "array" || len(args.Nodes) < 2 {
		return nil, false, errors.New("expected executable and role in ProgramArguments")
	}
	for _, a := range args.Nodes {
		if a.XMLName.Local != "string" {
			return nil, false, errors.New("ProgramArguments must contain only strings")
		}
	}
	if args.Nodes[1].Text != strings.TrimPrefix(label, "dev.getyip.") {
		return nil, false, errors.New("service role does not match its label")
	}
	program := args.Nodes[0].Text
	if !filepath.IsAbs(program) {
		return nil, false, errors.New("service executable must be an absolute path")
	}
	if err := verify(program); err != nil {
		return nil, false, err
	}
	if p := dict.get("Program"); p != nil {
		if p.XMLName.Local != "string" || p.Text != program {
			return nil, false, errors.New("Program and ProgramArguments disagree")
		}
		p.Text = destination
	}
	disabled := false
	if d := dict.get("Disabled"); d != nil {
		if d.XMLName.Local != "true" && d.XMLName.Local != "false" {
			return nil, false, errors.New("Disabled must be a boolean")
		}
		disabled = d.XMLName.Local == "true"
	}
	args.Nodes[0].Text = destination
	clearContainerText(root)
	encoded, err := xml.MarshalIndent(root, "", "  ")
	return append([]byte(xml.Header), append(encoded, '\n')...), disabled, err
}

func clearContainerText(n *plistNode) {
	if len(n.Nodes) > 0 {
		n.Text = ""
	}
	for i := range n.Nodes {
		clearContainerText(&n.Nodes[i])
	}
}
