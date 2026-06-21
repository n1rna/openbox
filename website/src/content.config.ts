// Astro content collection config.
//
// The docs collection loads from `../docs/` (the repo-root docs directory)
// rather than the Starlight default `src/content/docs/`. That keeps a single
// source of truth: every page on the website corresponds 1:1 to a markdown file
// you can read on GitHub.

import { glob } from "astro/loaders";
import { defineCollection } from "astro:content";
import { docsLoader } from "@astrojs/starlight/loaders";
import { docsSchema } from "@astrojs/starlight/schema";

// We point glob at our own path; docsLoader's default base is replaced but its
// schema/ID plumbing is reused via docsSchema().
void docsLoader;

export const collections = {
	docs: defineCollection({
		loader: glob({
			pattern: "**/*.{md,mdx}",
			base: "../docs",
		}),
		schema: docsSchema(),
	}),
};
