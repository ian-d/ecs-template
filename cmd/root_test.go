// NOTE
// ecs-templates is largely glue around go-getter and aws-sdk so the unit tests
// here just focues on moving files around, expected glob parsing, templated files,
// and manifest parsing.

package cmd

import (
	"os"
	"reflect"
	"strings"
	"testing"

	filet "github.com/Flaque/filet"
	getter "github.com/hashicorp/go-getter"
)

func init() {
	os.Setenv("ECS_TEMP_CANARY", "canary")
	pwd, _ = os.Getwd()
	getters := getter.Getters
	getters[`file`] = &getter.FileGetter{Copy: true}
	c = getter.Client{Pwd: pwd, Getters: getters}
}

var parseFilePairsTest = []struct {
	in       []string
	expected file
}{
	{[]string{` /foo , /bar   `}, file{`/foo`, `/bar`}},
	{[]string{` /{{env "ECS_TEMP_CANARY"}} , /bar   `}, file{`/canary`, `/bar`}},
	{[]string{` /foo `}, file{`/foo`, `/foo`}},
	{[]string{` /{{env "ECS_TEMP_CANARY"}} `}, file{`/canary`, `/canary`}},
}

func TestParseFilePairs(t *testing.T) {
	for _, tt := range parseFilePairsTest {
		actual, _ := parseFilePairs(tt.in)
		if actual[0] != tt.expected {
			t.Errorf("parseFilePairs(%s): expected %s, actual %s", tt.in, tt.expected, actual)
		}
	}
}

var parseGlobsTest = []struct {
	in       []string
	expected []file
}{
	{
		[]string{`../testdata/**/*.tmpl`},
		[]file{
			file{`../testdata/source/dir/test.tmpl`, `../testdata/source/dir/test.tmpl`},
			file{`../testdata/source/test.tmpl`, `../testdata/source/test.tmpl`},
		},
	},
}

func TestParseGlobs(t *testing.T) {
	for _, tt := range parseGlobsTest {
		actual, _ := parseGlobs(tt.in)
		if !reflect.DeepEqual(actual, tt.expected) {
			t.Errorf("parseGlobs(%s): expected %s, actual %s", tt.in, tt.expected, actual)
		}
	}
}

var fetchDirsTest = []struct {
	in       []file
	expected string
}{
	{[]file{file{`../testdata/source/dir`, ``}}, `test.tmpl`},
	{[]file{file{`../testdata/source/arch.tar.gz`, ``}}, `test.tmpl`},
}

func TestFetchDirs(t *testing.T) {
	defer filet.CleanUp(t)
	for _, tt := range fetchDirsTest {
		tmpDir := filet.TmpDir(t, "")
		tt.in[0].dest = tmpDir
		err := fetchDirectories(tt.in)
		if err != nil {
			t.Errorf("fetchDirectories(%s): %s", tt.in, err)
		}
		if !filet.DirContains(t, tmpDir, tt.expected) {
			t.Errorf("fetchDirectories(%s): expected %s/%s to exist", tt.in, tmpDir, tt.expected)
		}
	}
}

var fetchFilesTest = []struct {
	in       []file
	expected string
}{
	{[]file{file{`../testdata/source/test.tmpl`, ``}}, `canary.tmpl`},
}

func TestFetchFiles(t *testing.T) {
	defer filet.CleanUp(t)
	for _, tt := range fetchFilesTest {
		tmpDir := filet.TmpDir(t, "")
		tmpFilePath := tmpDir + `/` + tt.expected
		tt.in[0].dest = tmpFilePath
		err := fetchFiles(tt.in)
		if err != nil {
			t.Errorf("fetchFiles(%s): %s", tt.in, err)
		}
		if !filet.DirContains(t, tmpDir, tt.expected) {
			t.Errorf("fetchFiles(%s): expected %s/%s to exist", tt.in, tmpDir, tt.expected)
		}
	}
}

var parseFileDestinationTemplatesTest = []struct {
	in       []file
	expected []byte
}{
	{[]file{file{`../testdata/source/test.tmpl`, ``}}, []byte("canary\n")},
}

func TestParseFileDestinationTemplates(t *testing.T) {
	defer filet.CleanUp(t)
	for _, tt := range parseFileDestinationTemplatesTest {
		tmpDir := filet.TmpDir(t, "")
		tmpFilePath := tmpDir + `/test.tmpl`
		tt.in[0].dest = tmpFilePath
		//This is not good but i'd rather not muck w/ io.Copy
		if err := fetchFiles(tt.in); err != nil {
			t.Errorf("parseFileDestinationTemplates(%s): %s", tt.in, err)
		}
		if err := parseFileDestinationTemplates(tt.in); err != nil {
			t.Errorf("parseFileDestinationTemplates(%s): %s", tt.in, err)
		}
		if !filet.FileSays(t, tmpFilePath, tt.expected) {
			t.Errorf("fetchFiles(%s): expected %s to contain %s", tt.in, tmpFilePath, string(tt.expected))
		}
	}
}

var manifestTestYAML = `
dirs:
  - ../testdata/source/dir, $destDir/dir
  - ../testdata/source/arch.tar.gz, $destDir/arch
globs:
  - $destDir/dir/*.tmpl
  - $destDir/arch/*.tmpl
files:
  - ../testdata/source/test.tmpl,$destDir/test.tmpl
`

func TestParseManifest(t *testing.T) {
	//defer filet.CleanUp(t)
	//tmpManifestDir := filet.TmpDir(t, "")
	tmpDestDir := filet.TmpDir(t, "")
	manifestTestYAML := strings.Replace(manifestTestYAML, `$destDir`, tmpDestDir, -1)
	yamlFile := filet.TmpFile(t, "", manifestTestYAML)
	files, err := parseManifests([]string{yamlFile.Name()})
	if err != nil {
		t.Errorf("parseManifests: %s", err)
	}

	// Check that the resulting directories exist:
	for _, v := range []string{tmpDestDir + `/dir`, tmpDestDir + `/arch`} {
		if !filet.Exists(t, v) {
			t.Errorf("parseManifests: expected directory %s to exist", v)
		}
	}

	// Check that the archive was unpacked:
	archFile := tmpDestDir + `/arch/test.tmpl`
	if !filet.Exists(t, archFile) {
		t.Errorf("parseManifests: expected file %s to exist", archFile)
	}

	// Check that the expanded blobs and file pair are in the results
	var expectedFiles = []file{
		file{tmpDestDir + `/dir/test.tmpl`, tmpDestDir + `/dir/test.tmpl`},
		file{tmpDestDir + `/arch/test.tmpl`, tmpDestDir + `/arch/test.tmpl`},
		file{`../testdata/source/test.tmpl`, tmpDestDir + `/test.tmpl`},
	}
	if !reflect.DeepEqual(files, expectedFiles) {
		t.Errorf("parseManifests: expected %s, actual %s", files, expectedFiles)
	}
}
