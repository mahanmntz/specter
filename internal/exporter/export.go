package exporter

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"specter/internal/ats"
	"specter/internal/signals"
	"specter/internal/storage"
)

// ExportBundle contains full scanned datasets for export.
type ExportBundle struct {
	Companies        []ats.CompanyMeta        `json:"companies"`
	Roles            []ats.JobPosting         `json:"roles"`
	EngineeringLeads []signals.EngineeringLead `json:"engineering_leads"`
	Leads            []signals.Lead           `json:"leads"`
}

// Export dumps the database records into stdout or a designated file in JSON or CSV.
func Export(ctx context.Context, store *storage.Store, format string, outputPath string) error {
	companies, err := store.ListCompanies(ctx)
	if err != nil {
		return err
	}
	roles, err := store.ListRoles(ctx, "")
	if err != nil {
		return err
	}
	engLeads, err := store.ListEngineeringLeads(ctx, "", false)
	if err != nil {
		return err
	}
	leads, err := store.ListLeads(ctx, false)
	if err != nil {
		return err
	}

	var writer io.Writer = os.Stdout
	if outputPath != "" {
		f, err := os.Create(outputPath)
		if err != nil {
			return fmt.Errorf("could not create output file: %w", err)
		}
		defer f.Close()
		writer = f
	}

	format = strings.ToLower(format)
	switch format {
	case "csv":
		return exportCSV(writer, leads)
	case "roles-csv":
		return ExportRolesCSV(writer, roles)
	case "json":
		fallthrough
	default:
		bundle := ExportBundle{
			Companies:        companies,
			Roles:            roles,
			EngineeringLeads: engLeads,
			Leads:            leads,
		}
		enc := json.NewEncoder(writer)
		enc.SetIndent("", "  ")
		return enc.Encode(bundle)
	}
}

// ExportRolesCSV exports roles with direct application links, remote policy, and contractor badges.
func ExportRolesCSV(w io.Writer, roles []ats.JobPosting) error {
	csvWriter := csv.NewWriter(w)
	defer csvWriter.Flush()

	header := []string{
		"ID", "Company Domain", "Company Name", "Title", "Apply URL", "Job URL", "Location",
		"Remote Policy", "Global Remote", "Contractor Friendly", "Compensation",
		"Seniority", "Department", "First Seen At",
	}
	if err := csvWriter.Write(header); err != nil {
		return err
	}

	for _, r := range roles {
		applyURL := r.ApplyURL
		if applyURL == "" {
			applyURL = r.URL
		}
		record := []string{
			r.ID,
			r.CompanyDomain,
			r.CompanyName,
			r.Title,
			applyURL,
			r.URL,
			r.Location,
			r.RemotePolicy,
			strconv.FormatBool(r.GlobalRemote),
			strconv.FormatBool(r.ContractorFriendly),
			r.Compensation,
			r.Seniority,
			r.Department,
			r.FirstSeenAt.Format("2006-01-02 15:04:05"),
		}
		if err := csvWriter.Write(record); err != nil {
			return err
		}
	}
	return nil
}

func exportCSV(w io.Writer, leads []signals.Lead) error {
	csvWriter := csv.NewWriter(w)
	defer csvWriter.Flush()

	// Header
	if err := csvWriter.Write([]string{"ID", "Name", "Email", "GitHub Username", "Profile URL", "Role Title", "Source", "Contacted", "Discovered At"}); err != nil {
		return err
	}

	for _, l := range leads {
		record := []string{
			strconv.FormatInt(l.ID, 10),
			l.Name,
			l.Email,
			l.GitHubUsername,
			l.ProfileURL,
			l.RoleTitle,
			l.Source,
			strconv.FormatBool(l.Contacted),
			l.DiscoveredAt.Format("2006-01-02 15:04:05"),
		}
		if err := csvWriter.Write(record); err != nil {
			return err
		}
	}

	return nil
}
