import { readdir, readFile } from "node:fs/promises";
import { extname, join, resolve } from "node:path";

const outputDirectory = resolve(process.argv[2] ?? "../marketing/assets/generated");
const forbiddenFragments = [
  "I used to open twenty tabs",
  "Not having to build the comparison myself",
  "The options that fit my budget came first",
  "I gave it a gift budget",
  "After I picked, the payment and the order just proceeded",
];

const files = await listFiles(outputDirectory);
for (const file of files) {
  if (![".js", ".css", ".html"].includes(extname(file))) continue;
  const content = await readFile(file, "utf8");
  const leaked = forbiddenFragments.find((fragment) => content.includes(fragment));
  if (leaked) {
    throw new Error(`Release marketing asset contains a development testimonial: ${leaked}`);
  }
}

async function listFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const nested = await Promise.all(entries.map((entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? listFiles(path) : [path];
  }));
  return nested.flat();
}
