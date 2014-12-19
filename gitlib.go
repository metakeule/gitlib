package gitlib

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Git represents the git command
type Git struct {
	BinaryPath string
	Env        []string
	Debug      bool
	Dir        string
	mu         *sync.Mutex
}

// NewGit returns a new git repo and an error if the git command could not be found inside the path
// the current environment is used for the git command
func NewGit(dir string) (g *Git, err error) {
	g = &Git{}
	g.mu = &sync.Mutex{}
	g.Dir, err = filepath.Abs(dir)
	if err != nil {
		return
	}
	g.Env = os.Environ()

	// GIT_DIR => .gitdb
	// GIT_WORK_TREE => RAMFS
	// GIT_OBJECT_DIRECTORY => may be FUSE FS or also ram, then we can backup via git push
	// g.Env = append(g.Env, "GIT_DIR=.gitdb")
	g.BinaryPath, err = exec.LookPath("git")
	return
}

func (g *Git) IsInitialized() bool {
	dir := filepath.Join(g.Dir, ".git")
	info, err := os.Stat(dir)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		panic(dir + " is no directory")
	}
	return true
}

// run the given commands, preventing other commands to be run at the same time, stopping
// at the first error and returning it
func (g *Git) Transaction(cmds ...func(*Transaction) error) error {
	// fmt.Println("starting transaction")
	g.mu.Lock()
	defer g.mu.Unlock()

	tr := &Transaction{g}

	var err error
	for _, cmd := range cmds {
		if err = cmd(tr); err != nil {
			return err
		}
	}
	return nil
}

type Transaction struct {
	*Git
}

// Cmd returns the command for the given params and the given directory
// using the path of the git binary and the existing environment variables
func (g *Transaction) Cmd(params ...string) (cmd *exec.Cmd) {
	if g.Debug {
		fmt.Printf("\n%s %s\n", g.BinaryPath, strings.Join(params, " "))
	}
	cmd = exec.Command(g.BinaryPath, params...)
	cmd.Env = g.Env
	cmd.Dir = g.Dir
	return cmd
}

// Exec runs the given params and returns the combined output of stdout and stderr and
// any errors
func (g *Transaction) Exec(params ...string) ([]byte, error) {
	cmd := g.Cmd(params...)
	return cmd.Output()
}

func (g *Transaction) returnString(cmd *exec.Cmd) (string, error) {
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *Transaction) Init() error {
	_, err := g.Exec("init")
	return err
}

func (g *Transaction) InitBare() error {
	_, err := g.Exec("init", "--bare")
	return err
}

//  git ls-files 'node/pools/a63/84389-70d7-4199-9d90-4b8b9ba8e3d6'
func (t *Transaction) IsFileKnown(filepath string) (bool, error) {
	// fmt.Println("checking for known file of path", filepath)
	files, err := t.LsFiles(filepath)
	if err != nil {
		return false, err
	}
	if len(files) != 1 {
		return false, nil
	}
	return files[0] == filepath, nil
}

// WriteHashObject writes the content of the given reader to the repository inside the given
// directory. It returns the sha1 hash on success and an error otherwise
func (g *Transaction) WriteHashObject(rd io.Reader) (string, error) {
	cmd := g.Cmd("hash-object", "-w", "--stdin")
	cmd.Stdin = rd
	return g.returnString(cmd)
}

func (t *Transaction) ResetToHead(path string) error {
	cmd := t.Cmd("reset", "HEAD", "--", path)
	return cmd.Run()
}

// git reset HEAD -- .
func (t *Transaction) ResetToHeadAll() error {
	return t.ResetToHead(".")
}

// WriteHashObjectFile writes the content of the given file to the repository inside the given
// directory. It returns the sha1 hash on success and an error otherwise
func (g *Transaction) WriteHashObjectFile(filePath string) (string, error) {
	cmd := g.Cmd("hash-object", "-w", filePath)
	return g.returnString(cmd)
}

// git ls-files 'node/a63/84389-70d7-4199-9d90-4b8b9ba8e3d6'
// LsFiles returns the file paths that could be found by the given wildcard
func (t *Transaction) LsFiles(wildcard string) ([]string, error) {
	cmd := t.Cmd("ls-files", wildcard)
	out, err := t.returnString(cmd)
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	// fmt.Printf("lsfiles out: %#v\n", out)
	res := strings.Split(out, "\n")
	return res, nil
}

// ReadCatFile reads the object with the given sha1 and writes it to the given writer
func (g *Transaction) ReadCatFile(sha1 string, wr io.Writer) error {
	cmd := g.Cmd("cat-file", "-p", sha1)
	cmd.Stdout = wr
	return cmd.Run()
}

func (g *Transaction) RmIndex(path string) error {
	cmd := g.Cmd("rm", "--cached", path)
	return cmd.Run()
}

// git cat-file -p HEAD:int.go
func (g *Transaction) ReadCatHeadFile(path string, wr io.Writer) error {
	return g.ReadCatFile("HEAD:"+path, wr)
}

// CatFileType returns the type of the object with the given sha1
func (g *Transaction) CatFileType(sha1 string) (string, error) {
	cmd := g.Cmd("cat-file", "-t", sha1)
	return g.returnString(cmd)
}

// CatFileTree reads the tree of the last commit on branch to the given writer
func (g *Transaction) ReadCatFileTree(branch string, wr io.Writer) error {
	cmd := g.Cmd("cat-file", "-p", branch+"^{tree}")
	cmd.Stdout = wr
	return cmd.Run()
}

/*
git update-index --add --cacheinfo 100644 83baae61804e65cc73a7201a7252750c76066a30 test.txt
In this case, you’re specifying a mode of 100644 , which means it’s a normal
file. Other options are 100755 , which means it’s an executable file; and 120000 ,
which specifies a symbolic link
*/

// UpdateIndexFile updates the index of the given file with the data of the given
// sha1
func (g *Transaction) UpdateIndexCache(sha1, filepath string) error {
	cmd := g.Cmd("update-index", "--cacheinfo", "100644", sha1, filepath)
	return cmd.Run()
}

func (g *Transaction) UpdateIndexCacheExecutable(sha1, filepath string) error {
	cmd := g.Cmd("update-index", "--cacheinfo", "100755", sha1, filepath)
	return cmd.Run()
}

func (g *Transaction) UpdateIndexCacheLink(sha1, filepath string) error {
	cmd := g.Cmd("update-index", "--cacheinfo", "120000", sha1, filepath)
	return cmd.Run()
}

func (g *Transaction) AddIndexCache(sha1, filepath string) error {
	cmd := g.Cmd("update-index", "--add", "--cacheinfo", "100644", sha1, filepath)
	return cmd.Run()
}

func (g *Transaction) AddIndexCacheExecutable(sha1, filepath string) error {
	cmd := g.Cmd("update-index", "--add", "--cacheinfo", "100755", sha1, filepath)
	return cmd.Run()
}

func (g *Transaction) AddIndexCacheLink(sha1, filepath string) error {
	cmd := g.Cmd("update-index", "--add", "--cacheinfo", "120000", sha1, filepath)
	return cmd.Run()
}

// WriteTree writes the index to a tree
func (g *Transaction) WriteTree() (string, error) {
	cmd := g.Cmd("write-tree")
	return g.returnString(cmd)
}

// git read-tree --prefix=bak d8329fc1cc938780ffdd9f94e0d364e0ea74f579
func (g *Transaction) ReadTree(prefix, sha1 string) error {
	cmd := g.Cmd("read-tree", "--prefix="+prefix, sha1)
	return cmd.Run()
}

// git commit-tree d8329f
func (g *Transaction) CommitTree(sha1, parent string, message io.Reader) (string, error) {
	// fmt.Printf("committing: %#v with parent %#v\n", sha1, parent)
	params := []string{"commit-tree", sha1}
	if parent != "" {
		params = append(params, "-p", parent)
	}
	cmd := g.Cmd(params...)
	cmd.Stdin = message
	return g.returnString(cmd)
}

func (g *Transaction) ShowHeadsRef(ref string) (string, error) {
	// git show-ref --hash --heads refs/heads/master
	cmd := g.Cmd("show-ref", "--hash", "--heads", "refs/heads/"+ref)
	return g.returnString(cmd)
}

// git update-ref refs/heads/master 1a410efbd13591db07496601ebc7a059dd55cfe9
func (g *Transaction) UpdateHeadsRef(ref, sha1 string) error {
	cmd := g.Cmd("update-ref", "refs/heads/"+ref, sha1)
	return cmd.Run()
}

func (g *Transaction) UpdateTagsRef(ref, sha1 string) error {
	cmd := g.Cmd("update-ref", "refs/tags/"+ref, sha1)
	return cmd.Run()
}

// git symbolic-ref HEAD
func (g *Transaction) GetSymbolicRef(symref string) (string, error) {
	cmd := g.Cmd("symbolic-ref", symref)
	return g.returnString(cmd)
}

// git symbolic-ref HEAD refs/heads/test
func (g *Transaction) SetSymbolicHeadsRef(symref, headsRef string) error {
	cmd := g.Cmd("symbolic-ref", symref, "refs/heads/"+headsRef)
	return cmd.Run()
}

func (g *Transaction) SetSymbolicTagsRef(symref, tagsRef string) error {
	cmd := g.Cmd("symbolic-ref", symref, "refs/tags/"+tagsRef)
	return cmd.Run()
}

// git tag -a v1.1 1a410efbd13591db07496601ebc7a059dd55cfe9 -m 'test tag'
func (g *Transaction) Tag(tag, sha1, message string) error {
	params := []string{"tag", tag, sha1}
	if message != "" {
		params = append(params, "-a", "-m", message)
	}
	cmd := g.Cmd(params...)
	return cmd.Run()
}

func (g *Transaction) Tags() ([]string, error) {
	// params := []string{"tag"}
	cmd := g.Cmd("tag")
	str, err := g.returnString(cmd)

	if err != nil {
		return nil, err
	}

	return strings.Split(str, "\n"), nil
}

// git gc --auto
func (g *Transaction) GC() error {
	cmd := g.Cmd("gc", "--auto")
	return cmd.Run()
}

func (g *Transaction) Fsck() error {
	cmd := g.Cmd("fsck")
	return cmd.Run()
}

func (g *Transaction) FsckFull(wr io.Writer) error {
	cmd := g.Cmd("fsck", "--full")
	cmd.Stdout = wr
	return cmd.Run()
}
