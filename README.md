# Development
Creating tags and pushing to GitHub for every single change is incredibly slow and tedious. The standard to handle this in Go is using **Go Workspaces (`go.work`)** or a local **`replace directive`**. This allows you to edit your separate packages locally, and your main application will see the changes instantly without pushing or tagging anything to GitHub.

---
## Use the Go Workspaces
`Go Workspaces` allows you to tell the Go compiler to look at a group of local folders simultaneously; it overrides all remote GitHub downloads for those packages while you are coding.

Assume your directory structure looks like this:
```
~/repos
  ├ fin-meta-repo
  | └ fin-finance
  |   └ src       (Main app)
  └ gp-meta-repo
      ├ go-middlewares/    (Package)
      └ go-sessions/       (Package)
```
1. Open your terminal and navigate to your top-level project root (`~/repos/fin-meta-repo/`).
2. Initialize a workspace by running:
    ```
    go work init
    ```

3. Add your main app and local packages to the worspace:
   ```
   go work use ./fin-finance/src
   go work use ../gp-meta-repo/go-middlewares
   go work use ../gp-meta-repo/go-sessions
   ```

A file name ***`go.work`*** will be created in your root folder. It will look like this:
```
go 1.26.4

use (
  ../gp-meta-repo/go-middlewares
  ../gp-meta-repo/go-sessions
  ./fin-finance/src
)
```

You can now open your IDE, make a change inside `go-sessions`, and immediately run `go run main.go` inside `fin-finance/src`. Go will use your local changes instantly. When you are finally ready for production, you tag and push your packages all at once.

Finally, you may want to ensure that the fle `go.work` does not get push to your git repository.

---
## Use a replace directive
You can explicitly force the `main module` to look at the local file system for dependency modules (`go-sessions`) by using the `replace directive`. However, because the `replace directive` modifies the file `go.mod`, you risk committing a local path, which may create problems for other developers and your remote CI/CD engine. Obviously, the greatest risk is that your local path does not exist in their environments, thereby breaking the build.

To debug the code inside a dependency module while running your `main module`, you need to tell the Go compiler to use the local copy of the dependency module instead of its cache, read-only version in `$GOPATH/pkg/mod`.

To debug the dependency module `go-sessions`, you need to do the following:
1. Edit the main `go.mod`.
2. Add a *replace directive* at the bottom of your `go.mod` file pointing to the relative or absolute path of your local dependency:
    ```
    module finance

    go 1.26.4

    ...

    require (
      github.com/juan-carlos-trimino/go-sessions go-sessions/v1.x.x
    )

    //Force Go to use your local copy for compiling and debugging.
    replace github.com/juan-carlos-trimino/go-sessions => ../../../gp-meta-repo/go-sessions/
    ```
3. Run Delve or your IDE Debugger.<br>
   Run your debugger from the main module; you can now open files from `../../../gp-meta-repo/go-sessions/`, insert breakpoints, and step into them. The folder path specified in your `replace directive` ***must exactly match*** the folder path you have open in your IDE.

Finally, remember to remove this line before pushing your `go.mod` file to production.

---
## Installation
This package does not use release tags. To avoid Go's default tag resolution behavior, you must always specify an explicit branch name or a Git commit hash.

### Target the Latest Version (Commit Hash) from the Production Branch
To pull the latest updates from the primary branch:
```
go get github.com/juan-carlos-trimino/go-sessions@main
```

### Target a Specific Version (Commit Hash)
To lock your project to a specific point in time for reproducible builds, pass the explicit Git commit hash (short or full SHA):
```
go get github.com/juan-carlos-trimino/go-sessions@4b258e8
```

### Target a Development or Feature Branch
To test unmerged changes or experimental work located on a separate branch (e.g., `dev` or `feature-xyz`):
```
go get github.com/juan-carlos-trimino/go-sessions@<branch-name>
```

---
## Tracking New Versions
Because this repository does not use traditional Git tags, major updates and versions are signaled directly within the repository's commit history.

### Commit Message Prefixes
You can easily scan the commit history to determine which snapshots are safe for production:
* `[v1.0.0] rel:` — Identifies stable version milestones recommended for production.
* `[v1.1.0] feat:` — Introduces a new feature or public capability.
* `[v1.1.1] fix:` — Patches a bug or structural issue.
* `[v1.1.2] docs:` — Indicates documentation-only updates with no behavioral changes.

### How to Find and Copy Version Hashes
Users can find and copy the required commit hashes directly from GitHub or via the terminal.

### Option A: Using the GitHub UI
1. Navigate to the repository page: `github.com/juan-carlos-trimino/go-sessions`.
2. Click on the **Commits** history link (located right above the file list on the right side).
3. Locate the `[vX.Y.Z] rel:` milestone commit you want.
4. Click the small **Copy full SHA** icon or grab the short 8-character hash listed next to the commit.

### Option B: Using the Local Terminal
If you have cloned the repository locally, list the commit history with short hashes by running:
```
git log --oneline
```

To display the last **m** commits:
```
git log --online -n m
```

To filter by **[vX**:
```
git log --oneline --grep='^\[v[0-9]'
```

Copy the required hash from the leftmost column (e.g., `a1b2c3d`) to use in your `go get` command.

---
## Quick Start

```
package main

import (
	"fmt"
	"github.com/juan-carlos-trimino/go-sessions"
)

func main() {
	// Your initial package configuration goes here
	fmt.Println("Sessions initialized successfully.")
}
```

---
## Production-Ready Checklist
Before deploying changes utilizing a specific commit version, ensure your application matches the following requirements:
* [ ] The dependency entry in `go.mod` matches the targeted Git commit hash.
* [ ] Your project's local `go.sum` file has been verified and committed alongside `go.mod`.
* [ ] Production deployments utilize a locked hash version rather than tracking moving branch targets like `@main`.

---
## License
This project is licensed under the Apache-2.0 License. See the LICENSE file for details.
