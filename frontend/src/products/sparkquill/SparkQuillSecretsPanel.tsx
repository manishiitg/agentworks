import { useEffect, useState } from "react";
import { SecretSelectionSection } from "../../components/secrets/SecretSelectionSection";
import { agentApi } from "../../services/api";
import { responseContent } from "../../utils/plannerFiles";
import { FAMILY_ROOT } from "./api/platform/workspace";
export function SparkQuillSecretsPanel() {
  const [selected, setSelected] = useState<string[]>([]);
  const [globals, setGlobals] = useState<string[]>([]);
  const [ready, setReady] = useState(false);
  const [error, setError] = useState("");
  const read = async () => {
    const result = responseContent(
      await agentApi.getPlannerFileContent(`${FAMILY_ROOT}/product.json`),
    );
    if (!result) throw new Error("Project configuration unavailable.");
    return JSON.parse(result.content);
  };
  useEffect(() => {
    let live = true;
    read()
      .then((doc) => {
        if (live) {
          setSelected(doc.capabilities?.selected_secrets ?? []);
          setGlobals(doc.capabilities?.selected_global_secret_names ?? []);
          setReady(true);
        }
      })
      .catch(() => {
        if (live) setError("Could not load project integrations.");
      });
    return () => {
      live = false;
    };
  }, []);
  const save = async (key: string, names: string[]) => {
    const doc = await read();
    doc.capabilities = { ...doc.capabilities, [key]: names };
    await agentApi.updatePlannerFile(
      `${FAMILY_ROOT}/product.json`,
      JSON.stringify(doc, null, 2) + "\n",
      "Update SparkQuill integrations",
    );
    if (key === "selected_secrets") setSelected(names);
    else setGlobals(names);
  };
  if (!ready)
    return (
      <p role={error ? "alert" : undefined}>{error || "Loading secrets…"}</p>
    );
  return (
    <SecretSelectionSection
      workflowPath={FAMILY_ROOT}
      selectedSecrets={selected}
      selectedGlobalSecrets={globals}
      onSecretChange={(names) => save("selected_secrets", names)}
      onGlobalSecretChange={(names) =>
        save("selected_global_secret_names", names ?? [])
      }
    />
  );
}
