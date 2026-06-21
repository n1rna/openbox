// @ts-check
import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";
import starlightThemeFlexoki from "starlight-theme-flexoki";
import tailwindcss from "@tailwindcss/vite";

// https://astro.build/config
export default defineConfig({
	site: "https://docs.opbx.net",
	integrations: [
		starlight({
			title: "openbox",
			description:
				"Personal box manager. Control any Linux or Mac server you can reach, run commands and containers on it, and let AI agents drive a whole fleet from any laptop.",
			social: [
				{
					icon: "github",
					label: "GitHub",
					href: "https://github.com/n1rna/openbox",
				},
			],
			plugins: [starlightThemeFlexoki()],
			editLink: {
				baseUrl: "https://github.com/n1rna/openbox/edit/main/",
			},
			sidebar: [
				{
					label: "Getting started",
					items: [
						{ label: "Introduction", slug: "" },
						{ label: "Install", slug: "install" },
						{ label: "Quick start", slug: "quick-start" },
					],
				},
				{
					label: "Reference",
					items: [
						{ label: "CLI", slug: "cli" },
						{ label: "Architecture", slug: "architecture" },
						{ label: "Mesh networking", slug: "mesh" },
					],
				},
				{
					label: "Operations",
					items: [{ label: "Hosted deployment", slug: "deploy" }],
				},
			],
		}),
	],
	vite: {
		plugins: [tailwindcss()],
	},
});
