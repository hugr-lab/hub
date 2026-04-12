from setuptools import setup

setup(
    name="agent-bridge",
    version="0.1.0",
    packages=["agent_bridge"],
    install_requires=["jupyter_server>=2.0"],
    entry_points={
        "jupyter_server.serverextension": [
            "agent_bridge = agent_bridge",
        ],
    },
)
