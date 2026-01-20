ARG IMAGE_VERSION
FROM ${IMAGE_VERSION}

WORKDIR /app

# Install development tools
RUN apt-get update && apt-get install -y bash git && rm -rf /var/lib/apt/lists/*

ENV PATH /app/node_modules/.bin:$PATH

EXPOSE 5173

CMD ["npm", "run", "dev", "--", "--host"]
