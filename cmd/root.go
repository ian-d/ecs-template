package cmd

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/ian-d/ecs-template/functions"

	"github.com/bmatcuk/doublestar"
	getter "github.com/hashicorp/go-getter"
	"github.com/otiai10/copy"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
)

var (
	filesFlag    []string
	dirFlag      []string
	globFlag     []string
	manifestFlag []string
	filePairs    []file
	dirPairs     []file
	quiet        bool
	pwd          string
	c            getter.Client
	err          error
)

type file struct {
	source string
	dest   string
}

type Manifest struct {
	Dirs  []string
	Globs []string
	Files []string
}

var rootCmd = &cobra.Command{
	Use:     "ecs-template",
	Short:   "ENV/KMS/SSM aware file templating for ECS.",
	Long:    ``,
	Version: `0.0.1`,
	PreRun:  mainPreRun,
	Run:     runRootCmd,
}

func Execute() {
	if err = rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.Flags().StringArrayVarP(&filesFlag, "file", `f`, nil, "File pairs (source, dest) to process as templates.")
	rootCmd.Flags().StringArrayVarP(&dirFlag, "dir", `d`, nil, "Directory/archive pairs (source, dest) to process as templates.")
	rootCmd.Flags().StringSliceVarP(&globFlag, "glob", `g`, nil, "Globs to process as in-place file templates.")
	rootCmd.Flags().StringSliceVarP(&manifestFlag, "manifest", `m`, nil, "Manifests file containing files, directories, and globs.")
	rootCmd.Flags().BoolVar(&quiet, "quiet", false, "Do not print any normal output.")
}

func mainPreRun(cmd *cobra.Command, args []string) {
	pwd, _ = os.Getwd()
	getters := getter.Getters
	getters[`file`] = &getter.FileGetter{Copy: true}
	c = getter.Client{Pwd: pwd, Getters: getters}
}

func runRootCmd(cmd *cobra.Command, args []string) {
	if len(filesFlag) == 0 &&
		len(dirFlag) == 0 &&
		len(globFlag) == 0 &&
		len(manifestFlag) == 0 {
		cmd.Help()
		os.Exit(0)
	}
	var dirs []file
	var files []file

	// Fetch and parse manifests.
	if files, err = parseManifests(manifestFlag); err != nil {
		log.Fatalf("Could not handle manifest: %s", err)
	}
	filePairs = append(filePairs, files...)

	// Fetch and pars dirs/archs
	if dirs, err = parseFilePairs(dirFlag); err != nil {
		log.Fatalf("Could not parse dir flags: %s", err)
	}
	dirPairs = append(dirPairs, dirs...)

	// Expand globs and parse as files.
	if files, err = parseGlobs(globFlag); err != nil {
		log.Fatalf("Could not parse glob flag: %s", err)
	}
	filePairs = append(filePairs, files...)

	// Finally parse file flags
	if files, err = parseFilePairs(filesFlag); err != nil {
		log.Fatalf("Could not parse file flags: %s", err)
	}
	filePairs = append(filePairs, files...)

	// Use go-getter to fetch/move archives/directories
	if err = fetchDirectories(dirPairs); err != nil {
		log.Fatalf("Could not fetch directories: %s", err)
	}

	// Use go-getter to get all files, including those from globs
	if err = fetchFiles(filePairs); err != nil {
		log.Fatalf("Could not fetch files: %s", err)
	}

	// Now all files / archives have been succesfully moved
	// parse the destinations as in-place templates
	if err = parseFileDestinationTemplates(filePairs); err != nil {
		log.Fatalf("Could not parse template file: %s", err)
	}
}

func parseManifests(manifests []string) ([]file, error) {
	var files []file

	// Fetch & parse all the manifests.
	for _, v := range manifests {
		c.Mode = getter.ClientModeFile // This may have been changed by fetchDirectories
		m := Manifest{}

		// Parse the manifest arguments as templates
		if v, err = execTemplateString(v); err != nil {
			return nil, err
		}

		if !quiet {
			log.Printf("Parsing template %s\n", v)
		}

		// Copy the manifest to a tempfile using go-getter in case manifest path is s3, etc
		tmp, err := ioutil.TempFile("", "")
		if err != nil {
			return nil, err
		}
		tmpFile := tmp.Name()
		tmp.Close()
		c.Src = v
		c.Dst = tmpFile
		if err = c.Get(); err != nil {
			return nil, err
		}

		// Unmarshal the manifest for dir/glob/file parsing
		buff, err := ioutil.ReadFile(tmpFile)
		if err != nil {
			return nil, err
		}

		err = yaml.UnmarshalStrict(buff, &m)
		if err != nil {
			return nil, err
		}

		// Fetch the directories as they're parsed because globs may immediately refer to them
		t, err := parseFilePairs(m.Dirs)
		if err != nil {
			return nil, err
		}
		if err = fetchDirectories(t); err != nil {
			return nil, err
		}

		// Parse and expand the globs and files to be fetched later
		t, err = parseGlobs(m.Globs)
		if err != nil {
			return nil, err
		}
		files = append(files, t...)

		t, err = parseFilePairs(m.Files)
		if err != nil {
			return nil, err
		}
		files = append(files, t...)
	}

	return files, nil
}

func parseFilePairs(pairs []string) ([]file, error) {
	var res []file
	for _, v := range pairs {
		pair := strings.Split(v, `,`)

		res = append(res, file{
			source: strings.TrimSpace(pair[0]),
			dest:   strings.TrimSpace(pair[len(pair)-1])})
	}

	for i := range res {
		if res[i].source, err = execTemplateString(res[i].source); err != nil {
			return nil, err
		}
		if res[i].dest, err = execTemplateString(res[i].dest); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func parseGlobs(globs []string) ([]file, error) {
	var globTmp []string
	var files []file
	for _, v := range globs {
		var f []string
		if f, err = doublestar.Glob(v); err != nil {
			return nil, err
		}
		if !quiet {
			for _, match := range f {
				log.Printf("Glob %s matched file %s\n", v, match)
			}
		}
		globTmp = append(globTmp, f...)
	}
	if files, err = parseFilePairs(globTmp); err != nil {
		return nil, err
	}
	return files, nil
}

func fetchDirectories(dirs []file) error {
	c.Mode = getter.ClientModeDir
	for _, d := range dirs {
		// If the source is actually a local directory, just copy it w/ os.copy
		// because go-getter will just create a symlink
		if info, err := os.Stat(d.source); err == nil && info.Mode().IsDir() {
			if !quiet {
				log.Printf("Copying directory %s to destination %s\n", d.source, d.dest)
			}
			if err = copy.Copy(d.source, d.dest); err != nil {
				return err
			}
			continue
		}

		// Otherwise it's an archive/s3/http etc and use go-getter
		if !quiet {
			log.Printf("Fetching source %s to destination %s\n", d.source, d.dest)
		}
		c.Src, c.Dst = d.source, d.dest
		if err := c.Get(); err != nil {
			return err
		}
	}
	return nil
}

func fetchFiles(files []file) error {
	c.Mode = getter.ClientModeFile
	for _, f := range files {
		// Don't fetch anything that's an in-place template
		if f.source == f.dest {
			continue
		}
		if !quiet {
			log.Printf("Fetching file %s to destination %s\n", f.source, f.dest)
		}
		c.Src, c.Dst = f.source, f.dest
		if err := c.Get(); err != nil {
			return err
		}
	}
	return nil
}

func parseFileDestinationTemplates(files []file) error {
	for _, v := range files {
		sourcePath, destPath := v.source, v.dest
		if !filepath.IsAbs(sourcePath) {
			sourcePath = filepath.Join(pwd, sourcePath)
		}
		if !filepath.IsAbs(destPath) {
			destPath = filepath.Join(pwd, destPath)
		}

		if err := execTemplateFile(v.dest, pwd); err != nil {
			return err
		}

		// Preserve the permissions of the source file -> destination file
		if sourcePath != destPath {
			if info, err := os.Stat(sourcePath); !os.IsNotExist(err) {
				os.Chmod(destPath, info.Mode())
			}
		}
	}
	return nil
}

func execTemplateString(body string) (string, error) {
	var sb bytes.Buffer
	t := template.Must(template.New("").Funcs(functions.FuncMap()).Parse(body))
	err := t.Execute(&sb, nil)
	return sb.String(), err
}

func execTemplateFile(fileName string, pwd string) error {
	if !quiet {
		log.Printf("Template parsing file: %s\n", fileName)
	}
	buff, err := ioutil.ReadFile(fileName)
	if err != nil {
		return err
	}

	body, err := execTemplateString(string(buff))
	if err != nil {
		return err
	}

	if err := ioutil.WriteFile(fileName, []byte(body), 600); err != nil {
		return err
	}

	return nil
}
