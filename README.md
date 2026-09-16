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


# Usage
