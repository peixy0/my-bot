FROM python:3.14-trixie

RUN \
    # 1. Define the file path for convenience
    FILE=/etc/apt/sources.list.d/debian.sources && \
    # 2. Backup the original file
    cp "$FILE" "$FILE.bak" && \
    # 3. Replace default URLs with Aliyun mirror
    #    Trixie uses 'deb.debian.org' and 'security.debian.org' inside this file
    # sed -i 's@deb.debian.org@mirrors.aliyun.com@g' "$FILE" && \
    # sed -i 's@security.debian.org@mirrors.aliyun.com@g' "$FILE" && \
    # 4. Install your packages
    apt-get update && apt-get install -y \
    bash \
    git \
    tmux \
    locales \
    nano \
    curl \
    ripgrep \
    jq && \
    # 5. Restore the original file (send it back)
    # cp "$FILE" "$FILE.aliyun" && \
    cp "$FILE.bak" "$FILE"

RUN locale-gen C.UTF-8

ENV LANG=C.UTF-8
ENV LC_ALL=C.UTF-8

# Setup node
RUN curl -fsSL https://fnm.vercel.app/install | bash
RUN ~/.local/share/fnm/fnm install --lts

# Set workspace as working directory
WORKDIR /workspace

# Volume for persistent workspace
VOLUME ["/workspace"]

# Run bash interactively to keep container alive
CMD ["bash"]
