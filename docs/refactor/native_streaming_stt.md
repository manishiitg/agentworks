# Native streaming speech-to-text

The shared implementation is `agent_go/pkg/voicestt`, used by server and local desktop dictation. It uses sherpa-onnx with the Nemotron streaming model.

Internal design history, evaluations, benchmark reports and transcripts are maintained in the private deployments repository under `docs/voice-stt/`. The superseded Swift/CoreML and MLX design has been moved there.
