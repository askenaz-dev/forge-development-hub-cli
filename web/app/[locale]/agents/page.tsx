import { ComponentCatalog } from "@/components/component-catalog";

export default async function AgentsPage({
  searchParams,
}: {
  searchParams: Promise<{ q?: string }>;
}) {
  const { q } = await searchParams;
  return <ComponentCatalog kind="agent" basePath="/agents" q={q ?? ""} />;
}
