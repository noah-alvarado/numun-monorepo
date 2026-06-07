import js from "@eslint/js";
import tseslint from "typescript-eslint";

export default tseslint.config(
  // Ignore generated code (per APPLICATION.md §2: committed but not
  // hand-edited or hand-linted).
  {
    ignores: ["src/gen/**", "dist/**", "node_modules/**"],
  },
  // Baseline (per APPLICATION.md §9: ESLint defaults, no custom rules).
  js.configs.recommended,
  ...tseslint.configs.recommended,
  // SECURITY.md §2.2: no innerHTML on user-controlled values in the portal.
  // Lint-enforced; can be lifted with a per-line override + comment if a
  // future trusted-HTML helper lands.
  {
    files: ["src/**/*.{ts,tsx}"],
    rules: {
      "no-restricted-properties": [
        "error",
        {
          object: "Element.prototype",
          property: "innerHTML",
          message:
            "Don't assign innerHTML on user-controlled values (SECURITY.md §2.2).",
        },
      ],
    },
  },
);
