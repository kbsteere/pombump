package pombump

import (
	"fmt"
	"os"

	"github.com/chainguard-dev/gopom"
	"github.com/chainguard-dev/pombump/pkg"
	"github.com/ghodss/yaml"
	"github.com/spf13/cobra"
)

type analyzeCLIFlags struct {
	patches          string
	patchFile        string
	outputFormat     string
	outputDeps       string
	outputProperties string
	searchProperties bool
}

var analyzeFlags analyzeCLIFlags

func AnalyzeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyze <pom-file>",
		Short: "Analyze a POM file to understand dependency structure",
		Long: `Analyze a POM file to understand how dependencies are defined.
This command helps determine whether to use direct dependency patches or property updates.

Examples:
  # Analyze a POM and show report
  pombump analyze pom.xml

  # Analyze with proposed patches to see recommendations
  pombump analyze pom.xml --patches "io.netty@netty-codec-http@4.1.94.Final"

  # Analyze with multiple patches
  pombump analyze pom.xml --patches "io.netty@netty-codec-http@4.1.94.Final io.netty@netty-handler@4.1.94.Final"

  # Generate patch files based on analysis (appends to existing files)
  pombump analyze pom.xml --patches "io.netty@netty-codec-http@4.1.94.Final" \
    --output-deps pombump-deps.yaml \
    --output-properties pombump-properties.yaml
    
  # Search for properties in entire project tree
  pombump analyze pom.xml --search-properties --patches "org.assertj@assertj-core@3.25.0"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Analyze the project (with property search if requested)
			var analysis *pkg.AnalysisResult
			var err error

			if analyzeFlags.searchProperties {
				// Use enhanced analysis that searches for properties
				analysis, err = pkg.AnalyzeProjectPath(cmd.Context(), args[0])
				if err != nil {
					return fmt.Errorf("failed to analyze project: %w", err)
				}
			} else {
				// Use basic analysis (single file only)
				parsedPom, err := gopom.Parse(args[0])
				if err != nil {
					return fmt.Errorf("failed to parse POM file: %w", err)
				}

				analysis, err = pkg.AnalyzeProject(cmd.Context(), parsedPom)
				if err != nil {
					return fmt.Errorf("failed to analyze project: %w", err)
				}
			}

			// Process patches if provided
			var directPatches []pkg.Patch
			var propertyPatches map[string]string

			if analyzeFlags.patches != "" || analyzeFlags.patchFile != "" {
				patches, err := pkg.ParsePatches(cmd.Context(), analyzeFlags.patchFile, analyzeFlags.patches)
				if err != nil {
					return fmt.Errorf("failed to parse patches: %w", err)
				}

				directPatches, propertyPatches = pkg.PatchStrategy(cmd.Context(), analysis, patches)
			}

			// Convert to structured output format
			output := analysis.ToAnalysisOutput(args[0], directPatches, propertyPatches)

			// Handle different output formats
			if analyzeFlags.outputFormat == "json" || analyzeFlags.outputFormat == "yaml" {
				// Use structured output
				err = output.Write(analyzeFlags.outputFormat, os.Stdout)
				if err != nil {
					return fmt.Errorf("failed to write output: %w", err)
				}
			} else {
				// Use human-readable output
				err = output.Write("human", os.Stdout)
				if err != nil {
					return fmt.Errorf("failed to write output: %w", err)
				}
			}

			// Write patch files if requested (backward compatibility)
			if analyzeFlags.outputDeps != "" && len(directPatches) > 0 {
				if err := writeDepsFile(analyzeFlags.outputDeps, directPatches); err != nil {
					return fmt.Errorf("failed to write deps file: %w", err)
				}
				if analyzeFlags.outputFormat != "json" && analyzeFlags.outputFormat != "yaml" {
					fmt.Printf("\nWrote %d patches to %s\n", len(directPatches), analyzeFlags.outputDeps)
				}
			}

			if analyzeFlags.outputProperties != "" && len(propertyPatches) > 0 {
				if err := writePropertiesFile(analyzeFlags.outputProperties, propertyPatches); err != nil {
					return fmt.Errorf("failed to write properties file: %w", err)
				}
				if analyzeFlags.outputFormat != "json" && analyzeFlags.outputFormat != "yaml" {
					fmt.Printf("Wrote %d properties to %s\n", len(propertyPatches), analyzeFlags.outputProperties)
				}
			}

			return nil
		},
	}

	flagSet := cmd.Flags()
	flagSet.StringVar(&analyzeFlags.patches, "patches", "", "Space-separated list of patches to analyze (groupID@artifactID@version)")
	flagSet.StringVar(&analyzeFlags.patchFile, "patch-file", "", "File containing patches to analyze")
	flagSet.StringVar(&analyzeFlags.outputFormat, "output", "human", "Output format: human, json, or yaml")
	flagSet.StringVar(&analyzeFlags.outputDeps, "output-deps", "", "Write recommended dependency patches to this file")
	flagSet.StringVar(&analyzeFlags.outputProperties, "output-properties", "", "Write recommended property patches to this file")
	flagSet.BoolVar(&analyzeFlags.searchProperties, "search-properties", false, "Search for properties in nearby POM files")

	return cmd
}

func writeDepsFile(filename string, patches []pkg.Patch) error {
	// Read existing file if it exists
	var existingList pkg.PatchList
	if data, err := os.ReadFile(filename); err == nil {
		if err := yaml.Unmarshal(data, &existingList); err != nil {
			// If unmarshal fails, start fresh
			existingList = pkg.PatchList{Patches: []pkg.Patch{}}
		}
	}

	// Create a map to track existing patches by groupID:artifactID
	patchMap := make(map[string]pkg.Patch)
	for _, p := range existingList.Patches {
		key := fmt.Sprintf("%s:%s", p.GroupID, p.ArtifactID)
		patchMap[key] = p
	}

	// Update existing or add new patches
	for _, patch := range patches {
		key := fmt.Sprintf("%s:%s", patch.GroupID, patch.ArtifactID)
		patchMap[key] = patch // This will update if exists, or add if new
	}

	// Convert map back to slice
	var finalPatches []pkg.Patch
	for _, patch := range patchMap {
		finalPatches = append(finalPatches, patch)
	}

	finalList := pkg.PatchList{Patches: finalPatches}
	data, err := yaml.Marshal(finalList)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0600)
}

func writePropertiesFile(filename string, properties map[string]string) error {
	// Read existing file if it exists
	var existingList pkg.PropertyList
	if data, err := os.ReadFile(filename); err == nil {
		if err := yaml.Unmarshal(data, &existingList); err != nil {
			// If unmarshal fails, start fresh
			existingList = pkg.PropertyList{Properties: []pkg.PropertyPatch{}}
		}
	}

	// Create a map to track existing properties
	propMap := make(map[string]string)
	for _, p := range existingList.Properties {
		propMap[p.Property] = p.Value
	}

	// Update existing or add new properties
	for k, v := range properties {
		propMap[k] = v // This will update if exists, or add if new
	}

	// Convert map back to slice
	var finalProperties []pkg.PropertyPatch
	for k, v := range propMap {
		finalProperties = append(finalProperties, pkg.PropertyPatch{
			Property: k,
			Value:    v,
		})
	}

	finalList := pkg.PropertyList{Properties: finalProperties}
	data, err := yaml.Marshal(finalList)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0600)
}
