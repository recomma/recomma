#!/usr/bin/env node
/* eslint-env node */

import { copyFile, mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import favicons from "favicons";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const projectRoot = resolve(__dirname, "..");
const sourceLogo = join(projectRoot, "favicon", "logo.svg");
const publicDir = join(projectRoot, "public");
const indexHtmlPath = join(projectRoot, "index.html");

const markers = {
  start: "      <!-- favicons:start -->",
  end: "      <!-- favicons:end -->",
};

const indent = markers.start.slice(0, markers.start.indexOf("<"));

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

async function writeArtifacts({ images, files }) {
  const writes = [];

  await mkdir(publicDir, { recursive: true });

  for (const image of images) {
    writes.push(writeFile(join(publicDir, image.name), image.contents));
  }

  for (const file of files) {
    writes.push(writeFile(join(publicDir, file.name), file.contents));
  }

  await Promise.all(writes);
}

async function updateIndexHtml(htmlSnippet) {
  const existing = await readFile(indexHtmlPath, "utf8");
  const pattern = new RegExp(
    `${escapeRegExp(markers.start)}[\\s\\S]*?${escapeRegExp(markers.end)}`,
    "m",
  );

  if (!pattern.test(existing)) {
    throw new Error(
      `Missing favicon markers in ${indexHtmlPath}. Expected lines:\n${markers.start}\n${markers.end}`,
    );
  }

  const formattedSnippet = htmlSnippet
    .map((line) => `${indent}${line}`)
    .join("\n");

  const nextHtml = existing.replace(
    pattern,
    `${markers.start}\n${formattedSnippet}\n${markers.end}`,
  );

  await writeFile(indexHtmlPath, nextHtml);
}

async function main() {
  try {
    const response = await favicons(sourceLogo, {
      path: "/",
      appName: "Recomma",
      appShortName: "Recomma",
      appDescription: "Recomma web application",
      developerName: null,
      developerURL: null,
      background: "#ffffff",
      theme_color: "#0f172a",
      manifestMaskable: true,
    });

    await writeArtifacts(response);
    await copyFile(sourceLogo, join(publicDir, "favicon.svg"));
    await updateIndexHtml([
      ...response.html,
      '<link rel="icon" type="image/svg+xml" href="/favicon.svg">',
    ]);

    console.log(
      `Favicons generated: ${response.images.length} images, ${response.files.length} files, plus favicon.svg.`,
    );
  } catch (error) {
    console.error("Failed to generate favicons.");
    console.error(error instanceof Error ? error.message : error);
    process.exitCode = 1;
  }
}

await main();
