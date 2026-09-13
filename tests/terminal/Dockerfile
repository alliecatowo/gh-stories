# A real graphics-capable terminal on a real (virtual) display.
#
# This exists because "the picture actually appeared" cannot be established by
# printing escape bytes. kitty renders the real Kitty graphics protocol here,
# on a real X display, and the capture is the display's actual framebuffer.
FROM debian:bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
        kitty \
        xvfb x11-utils xdotool imagemagick \
        libgl1-mesa-dri libglx-mesa0 mesa-utils \
        fonts-dejavu-core fonts-noto-color-emoji \
        ca-certificates procps \
    && rm -rf /var/lib/apt/lists/*

# kitty needs OpenGL; Mesa's software rasteriser provides it without a GPU.
ENV LIBGL_ALWAYS_SOFTWARE=1 \
    GALLIUM_DRIVER=llvmpipe \
    DISPLAY=:99 \
    TERM=xterm-kitty

WORKDIR /work
COPY capture.sh /usr/local/bin/capture.sh
RUN chmod +x /usr/local/bin/capture.sh
ENTRYPOINT ["/usr/local/bin/capture.sh"]
