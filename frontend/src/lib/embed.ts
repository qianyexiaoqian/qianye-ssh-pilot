// 向量模型（长期记忆用）的预设服务商。接口都是 OpenAI 兼容的 /embeddings。
export interface EmbedModel {
  id: string;
  /** 可以选择的向量维度；没有列出时由用户自己填写或使用模型默认值 */
  dims?: number[];
}

export interface EmbedPreset {
  id: string;
  name: string;
  note: string;
  baseUrl: string;
  models: EmbedModel[];
  /** 接口地址下面的补充说明 */
  urlHint?: string;
  /** 模型名称下面的补充说明 */
  modelHint?: string;
  /** 本地服务，不需要 API Key */
  local?: boolean;
}

export const embedPresets: EmbedPreset[] = [
  {
    id: "aliyun",
    name: "阿里云百炼",
    note: "通义千问向量模型（默认）",
    baseUrl: "https://dashscope.aliyuncs.com/compatible-mode/v1",
    models: [
      { id: "text-embedding-v4", dims: [2048, 1536, 1024, 768, 512, 256, 128, 64] },
      { id: "qwen3.7-text-embedding", dims: [2560, 2048, 1536, 1024, 768, 512, 256] },
      { id: "qwen3.7-text-embedding-flash", dims: [1024, 768, 512, 256] },
      { id: "text-embedding-v3", dims: [1024, 768, 512, 256, 128, 64] },
    ],
    urlHint: "北京地域的公共地址；也可以换成百炼控制台里业务空间的专属地址（以 /compatible-mode/v1 结尾）",
  },
  {
    id: "siliconflow",
    name: "硅基流动",
    note: "BGE、Qwen3 Embedding",
    baseUrl: "https://api.siliconflow.cn/v1",
    models: [{ id: "BAAI/bge-m3" }, { id: "Qwen/Qwen3-Embedding-8B" }, { id: "Qwen/Qwen3-Embedding-4B" }, { id: "Qwen/Qwen3-Embedding-0.6B" }],
    modelHint: "只有 Qwen3 系列支持指定维度，BGE 请保持“模型默认”",
  },
  {
    id: "zhipu",
    name: "智谱",
    note: "embedding-3",
    baseUrl: "https://open.bigmodel.cn/api/paas/v4",
    models: [{ id: "embedding-3", dims: [2048, 1024, 512, 256] }, { id: "embedding-2" }],
  },
  {
    id: "volc",
    name: "火山方舟",
    note: "豆包向量模型",
    baseUrl: "https://ark.cn-beijing.volces.com/api/v3",
    models: [],
    modelHint: "填写方舟控制台里的文本向量模型名称，或推理接入点 ID（ep-…）",
  },
  {
    id: "openai",
    name: "OpenAI",
    note: "text-embedding-3",
    baseUrl: "https://api.openai.com/v1",
    models: [{ id: "text-embedding-3-small" }, { id: "text-embedding-3-large" }],
  },
  {
    id: "ollama",
    name: "Ollama",
    note: "本地模型，无需 API Key",
    baseUrl: "http://localhost:11434/v1",
    models: [{ id: "bge-m3" }, { id: "nomic-embed-text" }],
    modelHint: "需要先在本机执行 ollama pull 下载对应的模型",
    local: true,
  },
  { id: "custom", name: "自定义", note: "任意 OpenAI 兼容的 /embeddings 接口", baseUrl: "", models: [] },
];

export const embedPresetOf = (id: string) => embedPresets.find((p) => p.id === id) ?? embedPresets[embedPresets.length - 1];
