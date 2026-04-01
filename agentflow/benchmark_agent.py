import time
import asyncio
from agent_flow import LegalAgentMachine
from llm_provider import OptimizedLLMProvider
from sys_scanner import get_system_info, recommend_model

async def benchmark_chinese_and_tools():
    print("=== Qwen2.5 MLX Benchmark (Chinese & Tools) ===")

    # Initialize Provider with hardware-appropriate model
    sys_info = get_system_info()
    model_name = recommend_model(sys_info)
    provider = OptimizedLLMProvider(model_name=model_name)
    
    # 1. Benchmark Chinese Generation
    chinese_prompt = "请简要解释2026年中国商业租赁法中关于租金减免的规定。"
    print(f"\n[Bench] Generating Chinese text...")
    start_time = time.time()
    response = provider.generate(chinese_prompt)
    end_time = time.time()
    
    latency = end_time - start_time
    char_count = len(response)
    chars_per_sec = char_count / latency
    
    print(f"[*] Latency: {latency:.2f}s")
    print(f"[*] Response Length: {char_count} chars")
    print(f"[*] Speed: {chars_per_sec:.2f} chars/sec")
    print(f"[*] Preview: {response[:100]}...")

    # 2. Benchmark Tool Call Decision (Simulated)
    tool_prompt = "客户想要在抖音上发布一个关于租赁纠纷的视频，请调用相应的工具。"
    print(f"\n[Bench] Simulating Tool Call Decision...")
    start_time = time.time()
    # In a real agentic loop, this would be a structured output or specific prompt
    tool_decision = provider.generate(f"If the user wants to publish a video, output 'CALL_DOUYIN'. Prompt: {tool_prompt}")
    end_time = time.time()
    
    tool_latency = end_time - start_time
    print(f"[*] Tool Decision Latency: {tool_latency:.2f}s")
    print(f"[*] Decision: {tool_decision.strip()}")

    # 3. Overall System Throughput
    print(f"\n[Summary] Total Benchmark Time: {latency + tool_latency:.2f}s")

if __name__ == "__main__":
    asyncio.run(benchmark_chinese_and_tools())
