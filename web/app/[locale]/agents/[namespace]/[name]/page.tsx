import { ComponentDetail } from "@/components/component-detail";

export default async function AgentDetailPage({
  params,
}: {
  params: Promise<{ namespace: string; name: string }>;
}) {
  const { namespace, name } = await params;
  return <ComponentDetail kind="agent" namespace={namespace} name={name} basePath="/agents" />;
}
