   We are making a command line app in go named rcptpixie
    - This app will rename pdf files that are receipts
    - The file name should be in format date - reciept total - vendor name - category
        - An example for a dinner receipt name would be `04-16-2025 - 102.11 - Revier Bistro - Food.pdf`
        - If it was a hotel receipt it would include the stay dates so first date to last date for example `04-02-2025 to 04-10-2025 - 2006.33 - Four Seasons - Lodging`
    - The app will use ollama to talk to a local llm model
        - The app will use the chat interface to talk with the llm through ollama
        - The app will need prompts for the model to pull out the relevant information
        - The app will take a parameter that is the model name and default to llama3.3
  Complete this app in its entirety