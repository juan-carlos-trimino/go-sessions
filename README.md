# Development
Creating tags and pushing to GitHub for every single change is incredibly slow and tedious. The standard to handle this in Go is using **Go Workspaces (`go.work`)** or a local **`replace` directive**. This allows you to edit your separate packages locally, and your main application will see the changes instantly without pushing or tagging anything to GitHub. Here you will be using the **Go Workspaces**.

Go Workspaces allow you to tell the Go compiler to look at a group of local folders simultaneously. It overrides all remote GitHub downloads for those packages while you are coding.

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
2. Initialize a workspace by running:<br>
   ***bash***
   ```
   go work init
   ```
3. Add your main app and local packages to the worspace:<br>
   ***bash***
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
You can now open your IDE, make a change inside `go-sessions`, and immediately run `go run main.go` inside `fin-finance/src`. Go will use your live local changes instantly. When you are finally ready for production, you tag and push your packages all at once.

Finally, you may want to add `go.work` to your `.gitignore` file so it doesn't get push to git.

# Debugging
To debug code inside a dependency module (`go-sessions`) while running your `main module`, you need to tell the Go compiler to use a local copy of `go-sessions` instead of its cache, read-only version in `$GOPATH/pkg/mod`.

## Use a replace Directive
You can explicitly force your `main module` to look at your local file system for `go-sessions`. The following steps will guide you:
1. Edit the main `go.mod`.
2. Add a *replace directive* at the bottom of your `go.mod` file pointing to the relative or absolute path of your local dependency:
   ```
   module finance

   go 1.26.4

   require (
     github.com/juan-carlos-trimino/go-sessions go-sessions/v1.x.x
   )

   //Force Go to use your local copy for compiling and debugging.
   replace github.com/juan-carlos-trimino/go-sessions => ../../../gp-meta-repo/go-sessions/
  ```
3. Run Delve or your IDE Debugger.
   Run your debugger from the main module; you can now open files from `../../../gp-meta-repo/go-sessions/`, insert breakpoints, and step into them. The folder path specified in your replace directive ***must exactly match*** the folder path you have open in your IDE.

Finally, remember to remove this line before pushing your `go.mod` file to production.

# Usage
Clone the repo.
```
git clone https://github.com/juan-carlos-trimino/go-sessions.git
```

Initialize Go.
```
cd go-sessions
```

Execute `go mod init github.com/{GitHub-Username}/{Repo-Name}`.
```
go mod init github.com/juan-carlos-trimino/go-sessions
```

Create the file `main.go` and add the code to it. Then commit and push the code.
```
git add .
git commit -m "initial commit."
git push origin main
```

Go uses `Git Tags` to manage versions of the code. Create the tag and push it; ensure the version is changed accordingly.
```
git tag "go-sessions/v1.0.0"
git push origin go-sessions/v1.0.0
```

To use the package, install it (`go get -u {copy the repo url from GitHub}`).
```
go get -u github.com/juan-carlos-trimino/go-sessions
```

Next, open the file that will use the package and add this line (`github.com/{GitHub-Username}/{Repo-Name}`).
```
import (
  "github.com/juan-carlos-trimino/go-sessions"
)
```

To upgrade/downgrade the version of the package, move to the root of the module's directory structure (where the `go.mod` file is located) and execute (`go get -u "{package-name}@{git-commit-hash}"` or `go get -u "{package-name}@{version}"`).
```
go get -u "github.com/juan-carlos-trimino/go-sessions@xxxxxxx"

# or

go get -u "github.com/juan-carlos-trimino/go-sessions@go-sessions/v1.x.x"
```

## Delete and reuse a Git tag
In Git, ***tags belong to the entire repository, not to individual packages***. Git does not track folders or packages independently when it comes to versioning; it only tracks the history of the entire project tree as single commits. Therefore, to prevent tags from overwriting each other, the standard convention in Go and modern development toolchains is to prefix the tag with the package's folder path.

With that said, to delete and reuse a Git tag (version) on both **your local machine and remote repository (e.g., GitHub or GitLab)**, you must force-delete the tag from **both locations** and then clear Go's proxy cache so it realizes the tag has changed.

Delete the tag from your local machine, but ONLY the module sessions version go-sessions/v1.1.1.
```
git tag -d go-sessions/v1.1.1
```

Delete the tag from the remote repository.
```
git push origin --delete go-sessions/v1.1.1
```

After you create the new tag, you can assign that same tag version to your current commit (or a specific commit) and push it back up:
```
# Tag the current HEAD commit with the reused name.
git tag go-sessions/v1.1.1

# Push the new tag to the remote repository.
git push origin go-sessions/v1.1.1
```

If you want to tag a specific older commit instead of your current HEAD, just add the commit hash to the end of the creation command:
```
git tag go-sessions/v1.1.1 xxxxxxx
```

From your main project directory, you must clear Go's local download cache on your machine so it deletes the old version of the tag. This wipes out all downloaded dependencies locally, forcing Go to redownload them cleanly from the internet the next time you build or run `go get`.
```
go clean -modcache
```

> [!**Warning**]
> Reusing tags can cause headaches for other developers on your team.
>
> If a coworker already pulled down the old `go-sessions/v1.1.1` tag, their local Git will ***refuse to update it automatically*** when they run `git pull`. They will still be pointing to the old commit, which can cause deployment or building mismatches.
>
> If you have teammates, your teammates must force-update their local tags by running:
> ```
> git fetch --tags --force
> ```

Move to the main project directory that *uses* your go-sessions package. Because Go utilizes a global proxy (`proxy.golang.org`) by default, the proxy might cache your old tag for up to 24 hours. To bypass the proxy and force Go to pull the absolute newest tag directly from your repository, use the ***GOPROXY=direct*** override flag when fetching it:
```
GOPROXY=direct go get github.com/juan-carlos-trimino/go-sessions@go-sessions/v1.1.1
```

Tidy up your go.mod references.
```
go mod
```



To delete all tags and start from v1.0.0, you need to execute a bulk local deletion, a remote purging command, and force Go to reset its historical validation trackers.

Delete all tags from your local machine.
```
git tag -d $(git tag)
```

Bulk-delete all tags from `GitHub`.
```
git push origin --delete $(git tag)
```

Create and push your fresh v1.0.0 tag.

Switch to your main project directory to clear the cache.

Wipe out your local Go module cache.
```
go clean -modcache
```

Tell Go to completely ignore the global checksum database tracking for your username.
```
export GOSUMDB=off
```

Pull down the newly re-rolled package directly from `GitHub`.
```
GOPROXY=direct go get github.com/juan-carlos-trimino/go-sessions@v1.0.0
```

Tidy your dependencies.
```
go mod tidy
```
