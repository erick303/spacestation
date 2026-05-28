package scan

// classifyDir maps a directory name to a Category if it should be treated as a
// regenerable build/cache artifact discovered during a project walk.
// Returns (CatOther, "", false) if the name is not a known artifact.
func classifyDir(name string) (Category, string, bool) {
	switch name {
	case "node_modules":
		return CatNodeModules, "JavaScript dependency tree. Recreated by `npm install` / `pnpm install` / `yarn`.", true
	case ".pnpm-store":
		return CatNodeModules, "pnpm content-addressable store. Recreated automatically.", true

	case ".next":
		return CatJSBuild, "Next.js build output. Recreated by `next build` / `next dev`.", true
	case ".nuxt":
		return CatJSBuild, "Nuxt build output. Recreated by `nuxt build`.", true
	case ".turbo":
		return CatJSBuild, "Turborepo cache. Recreated by `turbo run`.", true
	case ".vite":
		return CatJSBuild, "Vite cache. Recreated on next dev server start.", true
	case ".parcel-cache":
		return CatJSBuild, "Parcel cache. Recreated on next build.", true
	case "dist":
		return CatJSBuild, "Generic build output directory. Recreated by your build script.", true
	case "out":
		return CatJSBuild, "Generic build output directory (Next.js export, etc).", true

	case ".venv", "venv", ".virtualenv":
		return CatPython, "Python virtualenv. Recreated by `python -m venv` and re-install.", true
	case "__pycache__":
		return CatPython, "Python bytecode cache. Recreated automatically on import.", true
	case ".pytest_cache":
		return CatPython, "pytest cache. Recreated on next test run.", true
	case ".mypy_cache":
		return CatPython, "mypy type-check cache. Recreated on next check.", true
	case ".ruff_cache":
		return CatPython, "ruff lint cache. Recreated on next run.", true
	case ".tox":
		return CatPython, "tox virtual envs. Recreated by `tox`.", true

	case "target":
		// Could be Rust or Maven/Java. Caller can refine using sibling files; we
		// default to Rust since it's overwhelmingly more common to gobble disk.
		return CatRust, "Build target dir (Rust/Cargo or Java/Maven). Recreated by build.", true

	case ".gradle":
		return CatJVM, "Gradle project cache. Recreated by next gradle build.", true
	case "build":
		return CatJVM, "Generic build output (Gradle/CMake/etc). Recreated by build.", true

	case "DerivedData":
		return CatXcode, "Xcode derived data. Recreated by Xcode on next build.", true
	}
	return CatOther, "", false
}

// shouldSkipWalk returns true for directory names we should never descend into
// during project walks (e.g., VCS dirs, well-known irrelevant trees).
func shouldSkipWalk(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", ".idea", ".vscode":
		return true
	}
	return false
}
